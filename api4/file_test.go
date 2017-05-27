// Copyright (c) 2017-present Mattermost, Inc. All Rights Reserved.
// See License.txt for license information.

package api4

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/primefour/servers/app"
	"github.com/primefour/servers/model"
	"github.com/primefour/servers/store"
	"github.com/primefour/servers/utils"
)

func TestUploadFile(t *testing.T) {
	th := Setup().InitBasic().InitSystemAdmin()
	defer TearDown()
	Client := th.Client

	user := th.BasicUser
	channel := th.BasicChannel

	var uploadInfo *model.FileInfo
	var data []byte
	var err error
	if data, err = readTestFile("test.png"); err != nil {
		t.Fatal(err)
	} else if fileResp, resp := Client.UploadFile(data, channel.Id, "test.png"); resp.Error != nil {
		t.Fatal(resp.Error)
	} else if len(fileResp.FileInfos) != 1 {
		t.Fatal("should've returned a single file infos")
	} else {
		uploadInfo = fileResp.FileInfos[0]
	}

	// The returned file info from the upload call will be missing some fields that will be stored in the database
	if uploadInfo.CreatorId != user.Id {
		t.Fatal("file should be assigned to user")
	} else if uploadInfo.PostId != "" {
		t.Fatal("file shouldn't have a post")
	} else if uploadInfo.Path != "" {
		t.Fatal("file path should not be set on returned info")
	} else if uploadInfo.ThumbnailPath != "" {
		t.Fatal("file thumbnail path should not be set on returned info")
	} else if uploadInfo.PreviewPath != "" {
		t.Fatal("file preview path should not be set on returned info")
	}

	var info *model.FileInfo
	if result := <-app.Srv.Store.FileInfo().Get(uploadInfo.Id); result.Err != nil {
		t.Fatal(result.Err)
	} else {
		info = result.Data.(*model.FileInfo)
	}

	if info.Id != uploadInfo.Id {
		t.Fatal("file id from response should match one stored in database")
	} else if info.CreatorId != user.Id {
		t.Fatal("file should be assigned to user")
	} else if info.PostId != "" {
		t.Fatal("file shouldn't have a post")
	} else if info.Path == "" {
		t.Fatal("file path should be set in database")
	} else if info.ThumbnailPath == "" {
		t.Fatal("file thumbnail path should be set in database")
	} else if info.PreviewPath == "" {
		t.Fatal("file preview path should be set in database")
	}

	// This also makes sure that the relative path provided above is sanitized out
	expectedPath := fmt.Sprintf("teams/%v/channels/%v/users/%v/%v/test.png", FILE_TEAM_ID, channel.Id, user.Id, info.Id)
	if info.Path != expectedPath {
		t.Logf("file is saved in %v", info.Path)
		t.Fatalf("file should've been saved in %v", expectedPath)
	}

	expectedThumbnailPath := fmt.Sprintf("teams/%v/channels/%v/users/%v/%v/test_thumb.jpg", FILE_TEAM_ID, channel.Id, user.Id, info.Id)
	if info.ThumbnailPath != expectedThumbnailPath {
		t.Logf("file thumbnail is saved in %v", info.ThumbnailPath)
		t.Fatalf("file thumbnail should've been saved in %v", expectedThumbnailPath)
	}

	expectedPreviewPath := fmt.Sprintf("teams/%v/channels/%v/users/%v/%v/test_preview.jpg", FILE_TEAM_ID, channel.Id, user.Id, info.Id)
	if info.PreviewPath != expectedPreviewPath {
		t.Logf("file preview is saved in %v", info.PreviewPath)
		t.Fatalf("file preview should've been saved in %v", expectedPreviewPath)
	}

	// Wait a bit for files to ready
	time.Sleep(2 * time.Second)

	if err := cleanupTestFile(info); err != nil {
		t.Fatal(err)
	}

	_, resp := Client.UploadFile(data, model.NewId(), "test.png")
	CheckForbiddenStatus(t, resp)

	_, resp = th.SystemAdminClient.UploadFile(data, channel.Id, "test.png")
	CheckNoError(t, resp)

	enableFileAttachments := *utils.Cfg.FileSettings.EnableFileAttachments
	defer func() {
		*utils.Cfg.FileSettings.EnableFileAttachments = enableFileAttachments
	}()
	*utils.Cfg.FileSettings.EnableFileAttachments = false

	_, resp = th.SystemAdminClient.UploadFile(data, channel.Id, "test.png")
	CheckNotImplementedStatus(t, resp)
}

func TestGetFile(t *testing.T) {
	th := Setup().InitBasic().InitSystemAdmin()
	defer TearDown()
	Client := th.Client
	channel := th.BasicChannel

	if utils.Cfg.FileSettings.DriverName == "" {
		t.Skip("skipping because no file driver is enabled")
	}

	fileId := ""
	var sent []byte
	var err error
	if sent, err = readTestFile("test.png"); err != nil {
		t.Fatal(err)
	} else {
		fileResp, resp := Client.UploadFile(sent, channel.Id, "test.png")
		CheckNoError(t, resp)

		fileId = fileResp.FileInfos[0].Id
	}

	data, resp := Client.GetFile(fileId)
	CheckNoError(t, resp)

	if data == nil || len(data) == 0 {
		t.Fatal("should not be empty")
	}

	for i := range data {
		if data[i] != sent[i] {
			t.Fatal("received file didn't match sent one")
		}
	}

	_, resp = Client.GetFile("junk")
	CheckBadRequestStatus(t, resp)

	_, resp = Client.GetFile(model.NewId())
	CheckNotFoundStatus(t, resp)

	Client.Logout()
	_, resp = Client.GetFile(fileId)
	CheckUnauthorizedStatus(t, resp)

	_, resp = th.SystemAdminClient.GetFile(fileId)
	CheckNoError(t, resp)
}

func TestGetFileThumbnail(t *testing.T) {
	th := Setup().InitBasic().InitSystemAdmin()
	defer TearDown()
	Client := th.Client
	channel := th.BasicChannel

	if utils.Cfg.FileSettings.DriverName == "" {
		t.Skip("skipping because no file driver is enabled")
	}

	fileId := ""
	var sent []byte
	var err error
	if sent, err = readTestFile("test.png"); err != nil {
		t.Fatal(err)
	} else {
		fileResp, resp := Client.UploadFile(sent, channel.Id, "test.png")
		CheckNoError(t, resp)

		fileId = fileResp.FileInfos[0].Id
	}

	// Wait a bit for files to ready
	time.Sleep(2 * time.Second)

	data, resp := Client.GetFileThumbnail(fileId)
	CheckNoError(t, resp)

	if data == nil || len(data) == 0 {
		t.Fatal("should not be empty")
	}

	_, resp = Client.GetFileThumbnail("junk")
	CheckBadRequestStatus(t, resp)

	_, resp = Client.GetFileThumbnail(model.NewId())
	CheckNotFoundStatus(t, resp)

	Client.Logout()
	_, resp = Client.GetFileThumbnail(fileId)
	CheckUnauthorizedStatus(t, resp)

	otherUser := th.CreateUser()
	Client.Login(otherUser.Email, otherUser.Password)
	_, resp = Client.GetFileThumbnail(fileId)
	CheckForbiddenStatus(t, resp)

	Client.Logout()
	_, resp = th.SystemAdminClient.GetFileThumbnail(fileId)
	CheckNoError(t, resp)
}

func TestGetFileLink(t *testing.T) {
	th := Setup().InitBasic().InitSystemAdmin()
	defer TearDown()
	Client := th.Client
	channel := th.BasicChannel

	if utils.Cfg.FileSettings.DriverName == "" {
		t.Skip("skipping because no file driver is enabled")
	}

	enablePublicLink := utils.Cfg.FileSettings.EnablePublicLink
	publicLinkSalt := *utils.Cfg.FileSettings.PublicLinkSalt
	defer func() {
		utils.Cfg.FileSettings.EnablePublicLink = enablePublicLink
		*utils.Cfg.FileSettings.PublicLinkSalt = publicLinkSalt
	}()
	utils.Cfg.FileSettings.EnablePublicLink = true
	*utils.Cfg.FileSettings.PublicLinkSalt = model.NewId()

	fileId := ""
	if data, err := readTestFile("test.png"); err != nil {
		t.Fatal(err)
	} else {
		fileResp, resp := Client.UploadFile(data, channel.Id, "test.png")
		CheckNoError(t, resp)

		fileId = fileResp.FileInfos[0].Id
	}

	link, resp := Client.GetFileLink(fileId)
	CheckBadRequestStatus(t, resp)

	// Hacky way to assign file to a post (usually would be done by CreatePost call)
	store.Must(app.Srv.Store.FileInfo().AttachToPost(fileId, th.BasicPost.Id))

	utils.Cfg.FileSettings.EnablePublicLink = false
	_, resp = Client.GetFileLink(fileId)
	CheckNotImplementedStatus(t, resp)

	// Wait a bit for files to ready
	time.Sleep(2 * time.Second)

	utils.Cfg.FileSettings.EnablePublicLink = true
	link, resp = Client.GetFileLink(fileId)
	CheckNoError(t, resp)
	if link == "" {
		t.Fatal("should've received public link")
	}

	_, resp = Client.GetFileLink("junk")
	CheckBadRequestStatus(t, resp)

	_, resp = Client.GetFileLink(model.NewId())
	CheckNotFoundStatus(t, resp)

	Client.Logout()
	_, resp = Client.GetFileLink(fileId)
	CheckUnauthorizedStatus(t, resp)

	otherUser := th.CreateUser()
	Client.Login(otherUser.Email, otherUser.Password)
	_, resp = Client.GetFileLink(fileId)
	CheckForbiddenStatus(t, resp)

	Client.Logout()
	_, resp = th.SystemAdminClient.GetFileLink(fileId)
	CheckNoError(t, resp)

	if result := <-app.Srv.Store.FileInfo().Get(fileId); result.Err != nil {
		t.Fatal(result.Err)
	} else {
		cleanupTestFile(result.Data.(*model.FileInfo))
	}
}

func TestGetFilePreview(t *testing.T) {
	th := Setup().InitBasic().InitSystemAdmin()
	defer TearDown()
	Client := th.Client
	channel := th.BasicChannel

	if utils.Cfg.FileSettings.DriverName == "" {
		t.Skip("skipping because no file driver is enabled")
	}

	fileId := ""
	var sent []byte
	var err error
	if sent, err = readTestFile("test.png"); err != nil {
		t.Fatal(err)
	} else {
		fileResp, resp := Client.UploadFile(sent, channel.Id, "test.png")
		CheckNoError(t, resp)

		fileId = fileResp.FileInfos[0].Id
	}

	// Wait a bit for files to ready
	time.Sleep(2 * time.Second)

	data, resp := Client.GetFilePreview(fileId)
	CheckNoError(t, resp)

	if data == nil || len(data) == 0 {
		t.Fatal("should not be empty")
	}

	_, resp = Client.GetFilePreview("junk")
	CheckBadRequestStatus(t, resp)

	_, resp = Client.GetFilePreview(model.NewId())
	CheckNotFoundStatus(t, resp)

	Client.Logout()
	_, resp = Client.GetFilePreview(fileId)
	CheckUnauthorizedStatus(t, resp)

	otherUser := th.CreateUser()
	Client.Login(otherUser.Email, otherUser.Password)
	_, resp = Client.GetFilePreview(fileId)
	CheckForbiddenStatus(t, resp)

	Client.Logout()
	_, resp = th.SystemAdminClient.GetFilePreview(fileId)
	CheckNoError(t, resp)
}

func TestGetFileInfo(t *testing.T) {
	th := Setup().InitBasic().InitSystemAdmin()
	defer TearDown()
	Client := th.Client
	user := th.BasicUser
	channel := th.BasicChannel

	if utils.Cfg.FileSettings.DriverName == "" {
		t.Skip("skipping because no file driver is enabled")
	}

	fileId := ""
	var sent []byte
	var err error
	if sent, err = readTestFile("test.png"); err != nil {
		t.Fatal(err)
	} else {
		fileResp, resp := Client.UploadFile(sent, channel.Id, "test.png")
		CheckNoError(t, resp)

		fileId = fileResp.FileInfos[0].Id
	}

	// Wait a bit for files to ready
	time.Sleep(2 * time.Second)

	info, resp := Client.GetFileInfo(fileId)
	CheckNoError(t, resp)

	if err != nil {
		t.Fatal(err)
	} else if info.Id != fileId {
		t.Fatal("got incorrect file")
	} else if info.CreatorId != user.Id {
		t.Fatal("file should be assigned to user")
	} else if info.PostId != "" {
		t.Fatal("file shouldn't have a post")
	} else if info.Path != "" {
		t.Fatal("file path shouldn't have been returned to client")
	} else if info.ThumbnailPath != "" {
		t.Fatal("file thumbnail path shouldn't have been returned to client")
	} else if info.PreviewPath != "" {
		t.Fatal("file preview path shouldn't have been returned to client")
	} else if info.MimeType != "image/png" {
		t.Fatal("mime type should've been image/png")
	}

	_, resp = Client.GetFileInfo("junk")
	CheckBadRequestStatus(t, resp)

	_, resp = Client.GetFileInfo(model.NewId())
	CheckNotFoundStatus(t, resp)

	Client.Logout()
	_, resp = Client.GetFileInfo(fileId)
	CheckUnauthorizedStatus(t, resp)

	otherUser := th.CreateUser()
	Client.Login(otherUser.Email, otherUser.Password)
	_, resp = Client.GetFileInfo(fileId)
	CheckForbiddenStatus(t, resp)

	Client.Logout()
	_, resp = th.SystemAdminClient.GetFileInfo(fileId)
	CheckNoError(t, resp)
}

func TestGetPublicFile(t *testing.T) {
	th := Setup().InitBasic().InitSystemAdmin()
	defer TearDown()
	Client := th.Client
	channel := th.BasicChannel

	if utils.Cfg.FileSettings.DriverName == "" {
		t.Skip("skipping because no file driver is enabled")
	}

	enablePublicLink := utils.Cfg.FileSettings.EnablePublicLink
	publicLinkSalt := *utils.Cfg.FileSettings.PublicLinkSalt
	defer func() {
		utils.Cfg.FileSettings.EnablePublicLink = enablePublicLink
		*utils.Cfg.FileSettings.PublicLinkSalt = publicLinkSalt
	}()
	utils.Cfg.FileSettings.EnablePublicLink = true
	*utils.Cfg.FileSettings.PublicLinkSalt = GenerateTestId()

	fileId := ""
	if data, err := readTestFile("test.png"); err != nil {
		t.Fatal(err)
	} else {
		fileResp, resp := Client.UploadFile(data, channel.Id, "test.png")
		CheckNoError(t, resp)

		fileId = fileResp.FileInfos[0].Id
	}

	// Hacky way to assign file to a post (usually would be done by CreatePost call)
	store.Must(app.Srv.Store.FileInfo().AttachToPost(fileId, th.BasicPost.Id))

	result := <-app.Srv.Store.FileInfo().Get(fileId)
	info := result.Data.(*model.FileInfo)
	link := app.GeneratePublicLink(Client.Url, info)

	// Wait a bit for files to ready
	time.Sleep(2 * time.Second)

	if resp, err := http.Get(link); err != nil || resp.StatusCode != http.StatusOK {
		t.Log(link)
		t.Fatal("failed to get image with public link", err)
	}

	if resp, err := http.Get(link[:strings.LastIndex(link, "?")]); err == nil && resp.StatusCode != http.StatusBadRequest {
		t.Fatal("should've failed to get image with public link without hash", resp.Status)
	}

	utils.Cfg.FileSettings.EnablePublicLink = false
	if resp, err := http.Get(link); err == nil && resp.StatusCode != http.StatusNotImplemented {
		t.Fatal("should've failed to get image with disabled public link")
	}

	// test after the salt has changed
	utils.Cfg.FileSettings.EnablePublicLink = true
	*utils.Cfg.FileSettings.PublicLinkSalt = GenerateTestId()

	if resp, err := http.Get(link); err == nil && resp.StatusCode != http.StatusBadRequest {
		t.Fatal("should've failed to get image with public link after salt changed")
	}

	if resp, err := http.Get(link); err == nil && resp.StatusCode != http.StatusBadRequest {
		t.Fatal("should've failed to get image with public link after salt changed")
	}

	if err := cleanupTestFile(store.Must(app.Srv.Store.FileInfo().Get(fileId)).(*model.FileInfo)); err != nil {
		t.Fatal(err)
	}

	cleanupTestFile(info)
}
