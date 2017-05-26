// Copyright (c) 2016-present Mattermost, Inc. All Rights Reserved.
// See License.txt for license information.

package app

import (
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"time"

	l4g "github.com/alecthomas/log4go"
	"github.com/dyatlov/go-opengraph/opengraph"
	"github.com/primefour/servers/einterfaces"
	"github.com/primefour/servers/model"
	"github.com/primefour/servers/store"
	"github.com/primefour/servers/utils"
)

var (
	httpClient *http.Client

	httpTimeout       = time.Duration(5 * time.Second)
	linkWithTextRegex = regexp.MustCompile(`<([^<\|]+)\|([^>]+)>`)
)

func dialTimeout(network, addr string) (net.Conn, error) {
	return net.DialTimeout(network, addr, httpTimeout)
}

func init() {
	p, ok := os.LookupEnv("HTTP_PROXY")
	if ok {
		if u, err := url.Parse(p); err == nil {
			httpClient = &http.Client{
				Transport: &http.Transport{
					Proxy: http.ProxyURL(u),
					Dial:  dialTimeout,
				},
			}
			return
		}
	}

	httpClient = &http.Client{
		Timeout: httpTimeout,
	}
}

func CreatePostAsUser(post *model.Post) (*model.Post, *model.AppError) {
	// Check that channel has not been deleted
	var channel *model.Channel
	if result := <-Srv.Store.Channel().Get(post.ChannelId, true); result.Err != nil {
		err := model.NewLocAppError("CreatePostAsUser", "api.context.invalid_param.app_error", map[string]interface{}{"Name": "post.channel_id"}, result.Err.Error())
		err.StatusCode = http.StatusBadRequest
		return nil, err
	} else {
		channel = result.Data.(*model.Channel)
	}

	if channel.DeleteAt != 0 {
		err := model.NewLocAppError("createPost", "api.post.create_post.can_not_post_to_deleted.error", nil, "")
		err.StatusCode = http.StatusBadRequest
		return nil, err
	}

	if rp, err := CreatePost(post, channel.TeamId, true); err != nil {
		if err.Id == "api.post.create_post.root_id.app_error" ||
			err.Id == "api.post.create_post.channel_root_id.app_error" ||
			err.Id == "api.post.create_post.parent_id.app_error" {
			err.StatusCode = http.StatusBadRequest
		}

		return nil, err
	} else {
		// Update the LastViewAt only if the post does not have from_webhook prop set (eg. Zapier app)
		if _, ok := post.Props["from_webhook"]; !ok {
			if result := <-Srv.Store.Channel().UpdateLastViewedAt([]string{post.ChannelId}, post.UserId); result.Err != nil {
				l4g.Error(utils.T("api.post.create_post.last_viewed.error"), post.ChannelId, post.UserId, result.Err)
			}
		}

		return rp, nil
	}

}

func CreatePost(post *model.Post, teamId string, triggerWebhooks bool) (*model.Post, *model.AppError) {
	var pchan store.StoreChannel
	if len(post.RootId) > 0 {
		pchan = Srv.Store.Post().Get(post.RootId)
	}

	// Verify the parent/child relationships are correct
	if pchan != nil {
		if presult := <-pchan; presult.Err != nil {
			return nil, model.NewLocAppError("createPost", "api.post.create_post.root_id.app_error", nil, "")
		} else {
			list := presult.Data.(*model.PostList)
			if len(list.Posts) == 0 || !list.IsChannelId(post.ChannelId) {
				return nil, model.NewLocAppError("createPost", "api.post.create_post.channel_root_id.app_error", nil, "")
			}

			if post.ParentId == "" {
				post.ParentId = post.RootId
			}

			if post.RootId != post.ParentId {
				parent := list.Posts[post.ParentId]
				if parent == nil {
					return nil, model.NewLocAppError("createPost", "api.post.create_post.parent_id.app_error", nil, "")
				}
			}
		}
	}

	post.Hashtags, _ = model.ParseHashtags(post.Message)

	var rpost *model.Post
	if result := <-Srv.Store.Post().Save(post); result.Err != nil {
		return nil, result.Err
	} else {
		rpost = result.Data.(*model.Post)
	}

	if einterfaces.GetMetricsInterface() != nil {
		einterfaces.GetMetricsInterface().IncrementPostCreate()
	}

	if len(post.FileIds) > 0 {
		// There's a rare bug where the client sends up duplicate FileIds so protect against that
		post.FileIds = utils.RemoveDuplicatesFromStringArray(post.FileIds)

		for _, fileId := range post.FileIds {
			if result := <-Srv.Store.FileInfo().AttachToPost(fileId, post.Id); result.Err != nil {
				l4g.Error(utils.T("api.post.create_post.attach_files.error"), post.Id, post.FileIds, post.UserId, result.Err)
			}
		}

		if einterfaces.GetMetricsInterface() != nil {
			einterfaces.GetMetricsInterface().IncrementPostFileAttachment(len(post.FileIds))
		}
	}

	if err := handlePostEvents(rpost, teamId, triggerWebhooks); err != nil {
		return nil, err
	}

	return rpost, nil
}

func handlePostEvents(post *model.Post, teamId string, triggerWebhooks bool) *model.AppError {
	var tchan store.StoreChannel
	if len(teamId) > 0 {
		tchan = Srv.Store.Team().Get(teamId)
	}
	cchan := Srv.Store.Channel().Get(post.ChannelId, true)
	uchan := Srv.Store.User().Get(post.UserId)

	var team *model.Team
	if tchan != nil {
		if result := <-tchan; result.Err != nil {
			return result.Err
		} else {
			team = result.Data.(*model.Team)
		}
	} else {
		// Blank team for DMs
		team = &model.Team{}
	}

	var channel *model.Channel
	if result := <-cchan; result.Err != nil {
		return result.Err
	} else {
		channel = result.Data.(*model.Channel)
	}

	InvalidateCacheForChannel(channel)
	InvalidateCacheForChannelPosts(channel.Id)

	var user *model.User
	if result := <-uchan; result.Err != nil {
		return result.Err
	} else {
		user = result.Data.(*model.User)
	}

	if _, err := SendNotifications(post, team, channel, user); err != nil {
		return err
	}

	if triggerWebhooks {
		go func() {
			if err := handleWebhookEvents(post, team, channel, user); err != nil {
				l4g.Error(err.Error())
			}
		}()
	}

	return nil
}

// This method only parses and processes the attachments,
// all else should be set in the post which is passed
func parseSlackAttachment(post *model.Post, attachments []*model.SlackAttachment) {
	post.Type = model.POST_SLACK_ATTACHMENT

	for _, attachment := range attachments {
		attachment.Text = parseSlackLinksToMarkdown(attachment.Text)
		attachment.Pretext = parseSlackLinksToMarkdown(attachment.Pretext)

		for _, field := range attachment.Fields {
			if value, ok := field.Value.(string); ok {
				field.Value = parseSlackLinksToMarkdown(value)
			}
		}
	}
	post.AddProp("attachments", attachments)
}

func parseSlackLinksToMarkdown(text string) string {
	return linkWithTextRegex.ReplaceAllString(text, "[${2}](${1})")
}

func SendEphemeralPost(teamId, userId string, post *model.Post) *model.Post {
	post.Type = model.POST_EPHEMERAL

	// fill in fields which haven't been specified which have sensible defaults
	if post.Id == "" {
		post.Id = model.NewId()
	}
	if post.CreateAt == 0 {
		post.CreateAt = model.GetMillis()
	}
	if post.Props == nil {
		post.Props = model.StringInterface{}
	}

	message := model.NewWebSocketEvent(model.WEBSOCKET_EVENT_EPHEMERAL_MESSAGE, "", post.ChannelId, userId, nil)
	message.Add("post", post.ToJson())

	go Publish(message)

	return post
}

func UpdatePost(post *model.Post, safeUpdate bool) (*model.Post, *model.AppError) {
	if utils.IsLicensed {
		if *utils.Cfg.ServiceSettings.AllowEditPost == model.ALLOW_EDIT_POST_NEVER {
			err := model.NewAppError("UpdatePost", "api.post.update_post.permissions_denied.app_error", nil, "", http.StatusForbidden)
			return nil, err
		}
	}

	var oldPost *model.Post
	if result := <-Srv.Store.Post().Get(post.Id); result.Err != nil {
		return nil, result.Err
	} else {
		oldPost = result.Data.(*model.PostList).Posts[post.Id]

		if oldPost == nil {
			err := model.NewAppError("UpdatePost", "api.post.update_post.find.app_error", nil, "id="+post.Id, http.StatusBadRequest)
			return nil, err
		}

		if oldPost.UserId != post.UserId {
			err := model.NewAppError("UpdatePost", "api.post.update_post.permissions.app_error", nil, "oldUserId="+oldPost.UserId, http.StatusBadRequest)
			return nil, err
		}

		if oldPost.DeleteAt != 0 {
			err := model.NewAppError("UpdatePost", "api.post.update_post.permissions_details.app_error", map[string]interface{}{"PostId": post.Id}, "", http.StatusBadRequest)
			return nil, err
		}

		if oldPost.IsSystemMessage() {
			err := model.NewAppError("UpdatePost", "api.post.update_post.system_message.app_error", nil, "id="+post.Id, http.StatusBadRequest)
			return nil, err
		}

		if utils.IsLicensed {
			if *utils.Cfg.ServiceSettings.AllowEditPost == model.ALLOW_EDIT_POST_TIME_LIMIT && model.GetMillis() > oldPost.CreateAt+int64(*utils.Cfg.ServiceSettings.PostEditTimeLimit*1000) {
				err := model.NewAppError("UpdatePost", "api.post.update_post.permissions_time_limit.app_error", map[string]interface{}{"timeLimit": *utils.Cfg.ServiceSettings.PostEditTimeLimit}, "", http.StatusBadRequest)
				return nil, err
			}
		}
	}

	newPost := &model.Post{}
	*newPost = *oldPost

	newPost.Message = post.Message
	newPost.EditAt = model.GetMillis()
	newPost.Hashtags, _ = model.ParseHashtags(post.Message)

	if !safeUpdate {
		newPost.IsPinned = post.IsPinned
		newPost.HasReactions = post.HasReactions
		newPost.FileIds = post.FileIds
		newPost.Props = post.Props
	}

	if result := <-Srv.Store.Post().Update(newPost, oldPost); result.Err != nil {
		return nil, result.Err
	} else {
		rpost := result.Data.(*model.Post)

		sendUpdatedPostEvent(rpost)

		InvalidateCacheForChannelPosts(rpost.ChannelId)

		return rpost, nil
	}
}

func PatchPost(postId string, patch *model.PostPatch) (*model.Post, *model.AppError) {
	post, err := GetSinglePost(postId)
	if err != nil {
		return nil, err
	}

	post.Patch(patch)

	updatedPost, err := UpdatePost(post, false)
	if err != nil {
		return nil, err
	}

	sendUpdatedPostEvent(updatedPost)
	InvalidateCacheForChannelPosts(updatedPost.ChannelId)

	return updatedPost, nil
}

func sendUpdatedPostEvent(post *model.Post) {
	message := model.NewWebSocketEvent(model.WEBSOCKET_EVENT_POST_EDITED, "", post.ChannelId, "", nil)
	message.Add("post", post.ToJson())

	go Publish(message)
}

func GetPostsPage(channelId string, page int, perPage int) (*model.PostList, *model.AppError) {
	if result := <-Srv.Store.Post().GetPosts(channelId, page*perPage, perPage, true); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.(*model.PostList), nil
	}
}

func GetPosts(channelId string, offset int, limit int) (*model.PostList, *model.AppError) {
	if result := <-Srv.Store.Post().GetPosts(channelId, offset, limit, true); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.(*model.PostList), nil
	}
}

func GetPostsEtag(channelId string) string {
	return (<-Srv.Store.Post().GetEtag(channelId, true)).Data.(string)
}

func GetPostsSince(channelId string, time int64) (*model.PostList, *model.AppError) {
	if result := <-Srv.Store.Post().GetPostsSince(channelId, time, true); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.(*model.PostList), nil
	}
}

func GetSinglePost(postId string) (*model.Post, *model.AppError) {
	if result := <-Srv.Store.Post().GetSingle(postId); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.(*model.Post), nil
	}
}

func GetPostThread(postId string) (*model.PostList, *model.AppError) {
	if result := <-Srv.Store.Post().Get(postId); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.(*model.PostList), nil
	}
}

func GetFlaggedPosts(userId string, offset int, limit int) (*model.PostList, *model.AppError) {
	if result := <-Srv.Store.Post().GetFlaggedPosts(userId, offset, limit); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.(*model.PostList), nil
	}
}

func GetFlaggedPostsForTeam(userId, teamId string, offset int, limit int) (*model.PostList, *model.AppError) {
	if result := <-Srv.Store.Post().GetFlaggedPostsForTeam(userId, teamId, offset, limit); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.(*model.PostList), nil
	}
}

func GetFlaggedPostsForChannel(userId, channelId string, offset int, limit int) (*model.PostList, *model.AppError) {
	if result := <-Srv.Store.Post().GetFlaggedPostsForChannel(userId, channelId, offset, limit); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.(*model.PostList), nil
	}
}

func GetPermalinkPost(postId string, userId string) (*model.PostList, *model.AppError) {
	if result := <-Srv.Store.Post().Get(postId); result.Err != nil {
		return nil, result.Err
	} else {
		list := result.Data.(*model.PostList)

		if len(list.Order) != 1 {
			return nil, model.NewLocAppError("getPermalinkTmp", "api.post_get_post_by_id.get.app_error", nil, "")
		}
		post := list.Posts[list.Order[0]]

		var channel *model.Channel
		var err *model.AppError
		if channel, err = GetChannel(post.ChannelId); err != nil {
			return nil, err
		}

		if err = JoinChannel(channel, userId); err != nil {
			return nil, err
		}

		return list, nil
	}
}

func GetPostsBeforePost(channelId, postId string, page, perPage int) (*model.PostList, *model.AppError) {
	if result := <-Srv.Store.Post().GetPostsBefore(channelId, postId, perPage, page*perPage); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.(*model.PostList), nil
	}
}

func GetPostsAfterPost(channelId, postId string, page, perPage int) (*model.PostList, *model.AppError) {
	if result := <-Srv.Store.Post().GetPostsAfter(channelId, postId, perPage, page*perPage); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.(*model.PostList), nil
	}
}

func GetPostsAroundPost(postId, channelId string, offset, limit int, before bool) (*model.PostList, *model.AppError) {
	var pchan store.StoreChannel
	if before {
		pchan = Srv.Store.Post().GetPostsBefore(channelId, postId, limit, offset)
	} else {
		pchan = Srv.Store.Post().GetPostsAfter(channelId, postId, limit, offset)
	}

	if result := <-pchan; result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.(*model.PostList), nil
	}
}

func DeletePost(postId string) (*model.Post, *model.AppError) {
	if result := <-Srv.Store.Post().GetSingle(postId); result.Err != nil {
		result.Err.StatusCode = http.StatusBadRequest
		return nil, result.Err
	} else {
		post := result.Data.(*model.Post)

		if result := <-Srv.Store.Post().Delete(postId, model.GetMillis()); result.Err != nil {
			return nil, result.Err
		}

		message := model.NewWebSocketEvent(model.WEBSOCKET_EVENT_POST_DELETED, "", post.ChannelId, "", nil)
		message.Add("post", post.ToJson())

		go Publish(message)
		go DeletePostFiles(post)
		go DeleteFlaggedPosts(post.Id)

		InvalidateCacheForChannelPosts(post.ChannelId)

		return post, nil
	}
}

func DeleteFlaggedPosts(postId string) {
	if result := <-Srv.Store.Preference().DeleteCategoryAndName(model.PREFERENCE_CATEGORY_FLAGGED_POST, postId); result.Err != nil {
		l4g.Warn(utils.T("api.post.delete_flagged_post.app_error.warn"), result.Err)
		return
	}
}

func DeletePostFiles(post *model.Post) {
	if len(post.FileIds) != 0 {
		return
	}

	if result := <-Srv.Store.FileInfo().DeleteForPost(post.Id); result.Err != nil {
		l4g.Warn(utils.T("api.post.delete_post_files.app_error.warn"), post.Id, result.Err)
	}
}

func SearchPostsInTeam(terms string, userId string, teamId string, isOrSearch bool) (*model.PostList, *model.AppError) {
	paramsList := model.ParseSearchParams(terms)
	channels := []store.StoreChannel{}

	for _, params := range paramsList {
		params.OrTerms = isOrSearch
		// don't allow users to search for everything
		if params.Terms != "*" {
			channels = append(channels, Srv.Store.Post().Search(teamId, userId, params))
		}
	}

	posts := model.NewPostList()
	for _, channel := range channels {
		if result := <-channel; result.Err != nil {
			return nil, result.Err
		} else {
			data := result.Data.(*model.PostList)
			posts.Extend(data)
		}
	}

	return posts, nil
}

func GetFileInfosForPost(postId string, readFromMaster bool) ([]*model.FileInfo, *model.AppError) {
	pchan := Srv.Store.Post().GetSingle(postId)
	fchan := Srv.Store.FileInfo().GetForPost(postId, readFromMaster, true)

	var infos []*model.FileInfo
	if result := <-fchan; result.Err != nil {
		return nil, result.Err
	} else {
		infos = result.Data.([]*model.FileInfo)
	}

	if len(infos) == 0 {
		// No FileInfos were returned so check if they need to be created for this post
		var post *model.Post
		if result := <-pchan; result.Err != nil {
			return nil, result.Err
		} else {
			post = result.Data.(*model.Post)
		}

		if len(post.Filenames) > 0 {
			Srv.Store.FileInfo().InvalidateFileInfosForPostCache(postId)
			// The post has Filenames that need to be replaced with FileInfos
			infos = MigrateFilenamesToFileInfos(post)
		}
	}

	return infos, nil
}

func GetOpenGraphMetadata(url string) *opengraph.OpenGraph {
	og := opengraph.NewOpenGraph()

	res, err := httpClient.Get(url)
	if err != nil {
		l4g.Error("GetOpenGraphMetadata request failed for url=%v with err=%v", url, err.Error())
		return og
	}
	defer CloseBody(res)

	if err := og.ProcessHTML(res.Body); err != nil {
		l4g.Error("GetOpenGraphMetadata processing failed for url=%v with err=%v", url, err.Error())
	}

	return og
}
