// Copyright (c) 2017-present Mattermost, Inc. All Rights Reserved.
// See License.txt for license information.

package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	l4g "github.com/alecthomas/log4go"
	"github.com/disintegration/imaging"
	"github.com/primefour/servers/model"
	"github.com/primefour/servers/utils"
	s3 "github.com/minio/minio-go"
	"github.com/rwcarlsen/goexif/exif"
	_ "golang.org/x/image/bmp"
)

const (
	/*
	  EXIF Image Orientations
	  1        2       3      4         5            6           7          8

	  888888  888888      88  88      8888888888  88                  88  8888888888
	  88          88      88  88      88  88      88  88          88  88      88  88
	  8888      8888    8888  8888    88          8888888888  8888888888          88
	  88          88      88  88
	  88          88  888888  888888
	*/
	Upright            = 1
	UprightMirrored    = 2
	UpsideDown         = 3
	UpsideDownMirrored = 4
	RotatedCWMirrored  = 5
	RotatedCCW         = 6
	RotatedCCWMirrored = 7
	RotatedCW          = 8

	MaxImageSize = 6048 * 4032 // 24 megapixels, roughly 36MB as a raw image
)

func ReadFile(path string) ([]byte, *model.AppError) {
	if utils.Cfg.FileSettings.DriverName == model.IMAGE_DRIVER_S3 {
		endpoint := utils.Cfg.FileSettings.AmazonS3Endpoint
		accessKey := utils.Cfg.FileSettings.AmazonS3AccessKeyId
		secretKey := utils.Cfg.FileSettings.AmazonS3SecretAccessKey
		secure := *utils.Cfg.FileSettings.AmazonS3SSL
		s3Clnt, err := s3.New(endpoint, accessKey, secretKey, secure)
		if err != nil {
			return nil, model.NewLocAppError("ReadFile", "api.file.read_file.s3.app_error", nil, err.Error())
		}
		bucket := utils.Cfg.FileSettings.AmazonS3Bucket
		minioObject, err := s3Clnt.GetObject(bucket, path)
		defer minioObject.Close()
		if err != nil {
			return nil, model.NewLocAppError("ReadFile", "api.file.read_file.s3.app_error", nil, err.Error())
		}
		if f, err := ioutil.ReadAll(minioObject); err != nil {
			return nil, model.NewLocAppError("ReadFile", "api.file.read_file.s3.app_error", nil, err.Error())
		} else {
			return f, nil
		}
	} else if utils.Cfg.FileSettings.DriverName == model.IMAGE_DRIVER_LOCAL {
		if f, err := ioutil.ReadFile(utils.Cfg.FileSettings.Directory + path); err != nil {
			return nil, model.NewLocAppError("ReadFile", "api.file.read_file.reading_local.app_error", nil, err.Error())
		} else {
			return f, nil
		}
	} else {
		return nil, model.NewAppError("ReadFile", "api.file.read_file.configured.app_error", nil, "", http.StatusNotImplemented)
	}
}

func MoveFile(oldPath, newPath string) *model.AppError {
	if utils.Cfg.FileSettings.DriverName == model.IMAGE_DRIVER_S3 {
		endpoint := utils.Cfg.FileSettings.AmazonS3Endpoint
		accessKey := utils.Cfg.FileSettings.AmazonS3AccessKeyId
		secretKey := utils.Cfg.FileSettings.AmazonS3SecretAccessKey
		secure := *utils.Cfg.FileSettings.AmazonS3SSL
		s3Clnt, err := s3.New(endpoint, accessKey, secretKey, secure)
		if err != nil {
			return model.NewLocAppError("moveFile", "api.file.write_file.s3.app_error", nil, err.Error())
		}
		bucket := utils.Cfg.FileSettings.AmazonS3Bucket

		var copyConds = s3.NewCopyConditions()
		if err = s3Clnt.CopyObject(bucket, newPath, "/"+path.Join(bucket, oldPath), copyConds); err != nil {
			return model.NewLocAppError("moveFile", "api.file.move_file.delete_from_s3.app_error", nil, err.Error())
		}
		if err = s3Clnt.RemoveObject(bucket, oldPath); err != nil {
			return model.NewLocAppError("moveFile", "api.file.move_file.delete_from_s3.app_error", nil, err.Error())
		}
	} else if utils.Cfg.FileSettings.DriverName == model.IMAGE_DRIVER_LOCAL {
		if err := os.MkdirAll(filepath.Dir(utils.Cfg.FileSettings.Directory+newPath), 0774); err != nil {
			return model.NewLocAppError("moveFile", "api.file.move_file.rename.app_error", nil, err.Error())
		}

		if err := os.Rename(utils.Cfg.FileSettings.Directory+oldPath, utils.Cfg.FileSettings.Directory+newPath); err != nil {
			return model.NewLocAppError("moveFile", "api.file.move_file.rename.app_error", nil, err.Error())
		}
	} else {
		return model.NewLocAppError("moveFile", "api.file.move_file.configured.app_error", nil, "")
	}

	return nil
}

func WriteFile(f []byte, path string) *model.AppError {
	if utils.Cfg.FileSettings.DriverName == model.IMAGE_DRIVER_S3 {
		endpoint := utils.Cfg.FileSettings.AmazonS3Endpoint
		accessKey := utils.Cfg.FileSettings.AmazonS3AccessKeyId
		secretKey := utils.Cfg.FileSettings.AmazonS3SecretAccessKey
		secure := *utils.Cfg.FileSettings.AmazonS3SSL
		s3Clnt, err := s3.New(endpoint, accessKey, secretKey, secure)
		if err != nil {
			return model.NewLocAppError("WriteFile", "api.file.write_file.s3.app_error", nil, err.Error())
		}
		bucket := utils.Cfg.FileSettings.AmazonS3Bucket
		ext := filepath.Ext(path)

		if model.IsFileExtImage(ext) {
			_, err = s3Clnt.PutObject(bucket, path, bytes.NewReader(f), model.GetImageMimeType(ext))
		} else {
			_, err = s3Clnt.PutObject(bucket, path, bytes.NewReader(f), "binary/octet-stream")
		}
		if err != nil {
			return model.NewLocAppError("WriteFile", "api.file.write_file.s3.app_error", nil, err.Error())
		}
	} else if utils.Cfg.FileSettings.DriverName == model.IMAGE_DRIVER_LOCAL {
		if err := writeFileLocally(f, utils.Cfg.FileSettings.Directory+path); err != nil {
			return err
		}
	} else {
		return model.NewLocAppError("WriteFile", "api.file.write_file.configured.app_error", nil, "")
	}

	return nil
}

func writeFileLocally(f []byte, path string) *model.AppError {
	if err := os.MkdirAll(filepath.Dir(path), 0774); err != nil {
		directory, _ := filepath.Abs(filepath.Dir(path))
		return model.NewLocAppError("WriteFile", "api.file.write_file_locally.create_dir.app_error", nil, "directory="+directory+", err="+err.Error())
	}

	if err := ioutil.WriteFile(path, f, 0644); err != nil {
		return model.NewLocAppError("WriteFile", "api.file.write_file_locally.writing.app_error", nil, err.Error())
	}

	return nil
}

func openFileWriteStream(path string) (io.Writer, *model.AppError) {
	if utils.Cfg.FileSettings.DriverName == model.IMAGE_DRIVER_S3 {
		return nil, model.NewLocAppError("openFileWriteStream", "api.file.open_file_write_stream.s3.app_error", nil, "")
	} else if utils.Cfg.FileSettings.DriverName == model.IMAGE_DRIVER_LOCAL {
		if err := os.MkdirAll(filepath.Dir(utils.Cfg.FileSettings.Directory+path), 0774); err != nil {
			return nil, model.NewLocAppError("openFileWriteStream", "api.file.open_file_write_stream.creating_dir.app_error", nil, err.Error())
		}

		if fileHandle, err := os.Create(utils.Cfg.FileSettings.Directory + path); err != nil {
			return nil, model.NewLocAppError("openFileWriteStream", "api.file.open_file_write_stream.local_server.app_error", nil, err.Error())
		} else {
			fileHandle.Chmod(0644)
			return fileHandle, nil
		}
	}

	return nil, model.NewLocAppError("openFileWriteStream", "api.file.open_file_write_stream.configured.app_error", nil, "")
}

func closeFileWriteStream(file io.Writer) {
	file.(*os.File).Close()
}

func GetInfoForFilename(post *model.Post, teamId string, filename string) *model.FileInfo {
	// Find the path from the Filename of the form /{channelId}/{userId}/{uid}/{nameWithExtension}
	split := strings.SplitN(filename, "/", 5)
	if len(split) < 5 {
		l4g.Error(utils.T("api.file.migrate_filenames_to_file_infos.unexpected_filename.error"), post.Id, filename)
		return nil
	}

	channelId := split[1]
	userId := split[2]
	oldId := split[3]
	name, _ := url.QueryUnescape(split[4])

	if split[0] != "" || split[1] != post.ChannelId || split[2] != post.UserId || strings.Contains(split[4], "/") {
		l4g.Warn(utils.T("api.file.migrate_filenames_to_file_infos.mismatched_filename.warn"), post.Id, post.ChannelId, post.UserId, filename)
	}

	pathPrefix := fmt.Sprintf("teams/%s/channels/%s/users/%s/%s/", teamId, channelId, userId, oldId)
	path := pathPrefix + name

	// Open the file and populate the fields of the FileInfo
	var info *model.FileInfo
	if data, err := ReadFile(path); err != nil {
		l4g.Error(utils.T("api.file.migrate_filenames_to_file_infos.file_not_found.error"), post.Id, filename, path, err)
		return nil
	} else {
		var err *model.AppError
		info, err = model.GetInfoForBytes(name, data)
		if err != nil {
			l4g.Warn(utils.T("api.file.migrate_filenames_to_file_infos.info.app_error"), post.Id, filename, err)
		}
	}

	// Generate a new ID because with the old system, you could very rarely get multiple posts referencing the same file
	info.Id = model.NewId()
	info.CreatorId = post.UserId
	info.PostId = post.Id
	info.CreateAt = post.CreateAt
	info.UpdateAt = post.UpdateAt
	info.Path = path

	if info.IsImage() {
		nameWithoutExtension := name[:strings.LastIndex(name, ".")]
		info.PreviewPath = pathPrefix + nameWithoutExtension + "_preview.jpg"
		info.ThumbnailPath = pathPrefix + nameWithoutExtension + "_thumb.jpg"
	}

	return info
}

func FindTeamIdForFilename(post *model.Post, filename string) string {
	split := strings.SplitN(filename, "/", 5)
	id := split[3]
	name, _ := url.QueryUnescape(split[4])

	// This post is in a direct channel so we need to figure out what team the files are stored under.
	if result := <-Srv.Store.Team().GetTeamsByUserId(post.UserId); result.Err != nil {
		l4g.Error(utils.T("api.file.migrate_filenames_to_file_infos.teams.app_error"), post.Id, result.Err)
	} else if teams := result.Data.([]*model.Team); len(teams) == 1 {
		// The user has only one team so the post must've been sent from it
		return teams[0].Id
	} else {
		for _, team := range teams {
			path := fmt.Sprintf("teams/%s/channels/%s/users/%s/%s/%s", team.Id, post.ChannelId, post.UserId, id, name)
			if _, err := ReadFile(path); err == nil {
				// Found the team that this file was posted from
				return team.Id
			}
		}
	}

	return ""
}

var fileMigrationLock sync.Mutex

// Creates and stores FileInfos for a post created before the FileInfos table existed.
func MigrateFilenamesToFileInfos(post *model.Post) []*model.FileInfo {
	if len(post.Filenames) == 0 {
		l4g.Warn(utils.T("api.file.migrate_filenames_to_file_infos.no_filenames.warn"), post.Id)
		return []*model.FileInfo{}
	}

	cchan := Srv.Store.Channel().Get(post.ChannelId, true)

	// There's a weird bug that rarely happens where a post ends up with duplicate Filenames so remove those
	filenames := utils.RemoveDuplicatesFromStringArray(post.Filenames)

	var channel *model.Channel
	if result := <-cchan; result.Err != nil {
		l4g.Error(utils.T("api.file.migrate_filenames_to_file_infos.channel.app_error"), post.Id, post.ChannelId, result.Err)
		return []*model.FileInfo{}
	} else {
		channel = result.Data.(*model.Channel)
	}

	// Find the team that was used to make this post since its part of the file path that isn't saved in the Filename
	var teamId string
	if channel.TeamId == "" {
		// This post was made in a cross-team DM channel so we need to find where its files were saved
		teamId = FindTeamIdForFilename(post, filenames[0])
	} else {
		teamId = channel.TeamId
	}

	// Create FileInfo objects for this post
	infos := make([]*model.FileInfo, 0, len(filenames))
	if teamId == "" {
		l4g.Error(utils.T("api.file.migrate_filenames_to_file_infos.team_id.error"), post.Id, filenames)
	} else {
		for _, filename := range filenames {
			info := GetInfoForFilename(post, teamId, filename)
			if info == nil {
				continue
			}

			infos = append(infos, info)
		}
	}

	// Lock to prevent only one migration thread from trying to update the post at once, preventing duplicate FileInfos from being created
	fileMigrationLock.Lock()
	defer fileMigrationLock.Unlock()

	if result := <-Srv.Store.Post().Get(post.Id); result.Err != nil {
		l4g.Error(utils.T("api.file.migrate_filenames_to_file_infos.get_post_again.app_error"), post.Id, result.Err)
		return []*model.FileInfo{}
	} else if newPost := result.Data.(*model.PostList).Posts[post.Id]; len(newPost.Filenames) != len(post.Filenames) {
		// Another thread has already created FileInfos for this post, so just return those
		if result := <-Srv.Store.FileInfo().GetForPost(post.Id, true, false); result.Err != nil {
			l4g.Error(utils.T("api.file.migrate_filenames_to_file_infos.get_post_file_infos_again.app_error"), post.Id, result.Err)
			return []*model.FileInfo{}
		} else {
			l4g.Debug(utils.T("api.file.migrate_filenames_to_file_infos.not_migrating_post.debug"), post.Id)
			return result.Data.([]*model.FileInfo)
		}
	}

	l4g.Debug(utils.T("api.file.migrate_filenames_to_file_infos.migrating_post.debug"), post.Id)

	savedInfos := make([]*model.FileInfo, 0, len(infos))
	fileIds := make([]string, 0, len(filenames))
	for _, info := range infos {
		if result := <-Srv.Store.FileInfo().Save(info); result.Err != nil {
			l4g.Error(utils.T("api.file.migrate_filenames_to_file_infos.save_file_info.app_error"), post.Id, info.Id, info.Path, result.Err)
			continue
		}

		savedInfos = append(savedInfos, info)
		fileIds = append(fileIds, info.Id)
	}

	// Copy and save the updated post
	newPost := &model.Post{}
	*newPost = *post

	newPost.Filenames = []string{}
	newPost.FileIds = fileIds

	// Update Posts to clear Filenames and set FileIds
	if result := <-Srv.Store.Post().Update(newPost, post); result.Err != nil {
		l4g.Error(utils.T("api.file.migrate_filenames_to_file_infos.save_post.app_error"), post.Id, newPost.FileIds, post.Filenames, result.Err)
		return []*model.FileInfo{}
	} else {
		return savedInfos
	}
}

func GeneratePublicLink(siteURL string, info *model.FileInfo) string {
	hash := GeneratePublicLinkHash(info.Id, *utils.Cfg.FileSettings.PublicLinkSalt)
	return fmt.Sprintf("%s/files/%v/public?h=%s", siteURL, info.Id, hash)
}

func GeneratePublicLinkV3(siteURL string, info *model.FileInfo) string {
	hash := GeneratePublicLinkHash(info.Id, *utils.Cfg.FileSettings.PublicLinkSalt)
	return fmt.Sprintf("%s%s/public/files/%v/get?h=%s", siteURL, model.API_URL_SUFFIX_V3, info.Id, hash)
}

func GeneratePublicLinkHash(fileId, salt string) string {
	hash := sha256.New()
	hash.Write([]byte(salt))
	hash.Write([]byte(fileId))

	return base64.RawURLEncoding.EncodeToString(hash.Sum(nil))
}

func UploadFiles(teamId string, channelId string, userId string, fileHeaders []*multipart.FileHeader, clientIds []string) (*model.FileUploadResponse, *model.AppError) {
	if len(utils.Cfg.FileSettings.DriverName) == 0 {
		return nil, model.NewAppError("uploadFile", "api.file.upload_file.storage.app_error", nil, "", http.StatusNotImplemented)
	}

	resStruct := &model.FileUploadResponse{
		FileInfos: []*model.FileInfo{},
		ClientIds: []string{},
	}

	previewPathList := []string{}
	thumbnailPathList := []string{}
	imageDataList := [][]byte{}

	for i, fileHeader := range fileHeaders {
		file, fileErr := fileHeader.Open()
		defer file.Close()
		if fileErr != nil {
			return nil, model.NewAppError("UploadFiles", "api.file.upload_file.bad_parse.app_error", nil, fileErr.Error(), http.StatusBadRequest)
		}

		buf := bytes.NewBuffer(nil)
		io.Copy(buf, file)
		data := buf.Bytes()

		info, err := DoUploadFile(teamId, channelId, userId, fileHeader.Filename, data)
		if err != nil {
			return nil, err
		}

		if info.PreviewPath != "" || info.ThumbnailPath != "" {
			previewPathList = append(previewPathList, info.PreviewPath)
			thumbnailPathList = append(thumbnailPathList, info.ThumbnailPath)
			imageDataList = append(imageDataList, data)
		}

		resStruct.FileInfos = append(resStruct.FileInfos, info)

		if len(clientIds) > 0 {
			resStruct.ClientIds = append(resStruct.ClientIds, clientIds[i])
		}
	}

	HandleImages(previewPathList, thumbnailPathList, imageDataList)

	return resStruct, nil
}

func DoUploadFile(teamId string, channelId string, userId string, rawFilename string, data []byte) (*model.FileInfo, *model.AppError) {
	filename := filepath.Base(rawFilename)

	info, err := model.GetInfoForBytes(filename, data)
	if err != nil {
		err.StatusCode = http.StatusBadRequest
		return nil, err
	}

	info.Id = model.NewId()
	info.CreatorId = userId

	pathPrefix := "teams/" + teamId + "/channels/" + channelId + "/users/" + userId + "/" + info.Id + "/"
	info.Path = pathPrefix + filename

	if info.IsImage() {
		// Check dimensions before loading the whole thing into memory later on
		if info.Width*info.Height > MaxImageSize {
			err := model.NewLocAppError("uploadFile", "api.file.upload_file.large_image.app_error", map[string]interface{}{"Filename": filename}, "")
			err.StatusCode = http.StatusBadRequest
			return nil, err
		}

		nameWithoutExtension := filename[:strings.LastIndex(filename, ".")]
		info.PreviewPath = pathPrefix + nameWithoutExtension + "_preview.jpg"
		info.ThumbnailPath = pathPrefix + nameWithoutExtension + "_thumb.jpg"
	}

	if err := WriteFile(data, info.Path); err != nil {
		return nil, err
	}

	if result := <-Srv.Store.FileInfo().Save(info); result.Err != nil {
		return nil, result.Err
	}

	return info, nil
}

func HandleImages(previewPathList []string, thumbnailPathList []string, fileData [][]byte) {
	for i, data := range fileData {
		go func(i int, data []byte) {
			img, width, height := prepareImage(fileData[i])
			if img != nil {
				go generateThumbnailImage(*img, thumbnailPathList[i], width, height)
				go generatePreviewImage(*img, previewPathList[i], width)
			}
		}(i, data)
	}
}

func prepareImage(fileData []byte) (*image.Image, int, int) {
	// Decode image bytes into Image object
	img, imgType, err := image.Decode(bytes.NewReader(fileData))
	if err != nil {
		l4g.Error(utils.T("api.file.handle_images_forget.decode.error"), err)
		return nil, 0, 0
	}

	width := img.Bounds().Dx()
	height := img.Bounds().Dy()

	// Fill in the background of a potentially-transparent png file as white
	if imgType == "png" {
		dst := image.NewRGBA(img.Bounds())
		draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
		draw.Draw(dst, dst.Bounds(), img, img.Bounds().Min, draw.Over)
		img = dst
	}

	// Flip the image to be upright
	orientation, _ := getImageOrientation(fileData)

	switch orientation {
	case UprightMirrored:
		img = imaging.FlipH(img)
	case UpsideDown:
		img = imaging.Rotate180(img)
	case UpsideDownMirrored:
		img = imaging.FlipV(img)
	case RotatedCWMirrored:
		img = imaging.Transpose(img)
	case RotatedCCW:
		img = imaging.Rotate270(img)
	case RotatedCCWMirrored:
		img = imaging.Transverse(img)
	case RotatedCW:
		img = imaging.Rotate90(img)
	}

	return &img, width, height
}

func getImageOrientation(imageData []byte) (int, error) {
	if exifData, err := exif.Decode(bytes.NewReader(imageData)); err != nil {
		return Upright, err
	} else {
		if tag, err := exifData.Get("Orientation"); err != nil {
			return Upright, err
		} else {
			orientation, err := tag.Int(0)
			if err != nil {
				return Upright, err
			} else {
				return orientation, nil
			}
		}
	}
}

func generateThumbnailImage(img image.Image, thumbnailPath string, width int, height int) {
	thumbWidth := float64(utils.Cfg.FileSettings.ThumbnailWidth)
	thumbHeight := float64(utils.Cfg.FileSettings.ThumbnailHeight)
	imgWidth := float64(width)
	imgHeight := float64(height)

	var thumbnail image.Image
	if imgHeight < thumbHeight && imgWidth < thumbWidth {
		thumbnail = img
	} else if imgHeight/imgWidth < thumbHeight/thumbWidth {
		thumbnail = imaging.Resize(img, 0, utils.Cfg.FileSettings.ThumbnailHeight, imaging.Lanczos)
	} else {
		thumbnail = imaging.Resize(img, utils.Cfg.FileSettings.ThumbnailWidth, 0, imaging.Lanczos)
	}

	buf := new(bytes.Buffer)
	if err := jpeg.Encode(buf, thumbnail, &jpeg.Options{Quality: 90}); err != nil {
		l4g.Error(utils.T("api.file.handle_images_forget.encode_jpeg.error"), thumbnailPath, err)
		return
	}

	if err := WriteFile(buf.Bytes(), thumbnailPath); err != nil {
		l4g.Error(utils.T("api.file.handle_images_forget.upload_thumb.error"), thumbnailPath, err)
		return
	}
}

func generatePreviewImage(img image.Image, previewPath string, width int) {
	var preview image.Image
	if width > int(utils.Cfg.FileSettings.PreviewWidth) {
		preview = imaging.Resize(img, utils.Cfg.FileSettings.PreviewWidth, utils.Cfg.FileSettings.PreviewHeight, imaging.Lanczos)
	} else {
		preview = img
	}

	buf := new(bytes.Buffer)

	if err := jpeg.Encode(buf, preview, &jpeg.Options{Quality: 90}); err != nil {
		l4g.Error(utils.T("api.file.handle_images_forget.encode_preview.error"), previewPath, err)
		return
	}

	if err := WriteFile(buf.Bytes(), previewPath); err != nil {
		l4g.Error(utils.T("api.file.handle_images_forget.upload_preview.error"), previewPath, err)
		return
	}
}

func GetFileInfo(fileId string) (*model.FileInfo, *model.AppError) {
	if result := <-Srv.Store.FileInfo().Get(fileId); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.(*model.FileInfo), nil
	}
}
