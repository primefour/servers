// Copyright (c) 2016-present Mattermost, Inc. All Rights Reserved.
// See License.txt for license information.

package app

import (
	"crypto/tls"
	"io"
	"io/ioutil"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	l4g "github.com/alecthomas/log4go"
	"github.com/primefour/servers/einterfaces"
	"github.com/primefour/servers/model"
	"github.com/primefour/servers/store"
	"github.com/primefour/servers/utils"
)

const (
	TRIGGERWORDS_FULL       = 0
	TRIGGERWORDS_STARTSWITH = 1
)

func handleWebhookEvents(post *model.Post, team *model.Team, channel *model.Channel, user *model.User) *model.AppError {
	if !utils.Cfg.ServiceSettings.EnableOutgoingWebhooks {
		return nil
	}

	if channel.Type != model.CHANNEL_OPEN {
		return nil
	}

	hchan := Srv.Store.Webhook().GetOutgoingByTeam(team.Id, -1, -1)
	result := <-hchan
	if result.Err != nil {
		return result.Err
	}

	hooks := result.Data.([]*model.OutgoingWebhook)
	if len(hooks) == 0 {
		return nil
	}

	splitWords := strings.Fields(post.Message)
	if len(splitWords) == 0 {
		return nil
	}
	firstWord := splitWords[0]

	relevantHooks := []*model.OutgoingWebhook{}
	for _, hook := range hooks {
		if hook.ChannelId == post.ChannelId || len(hook.ChannelId) == 0 {
			if hook.ChannelId == post.ChannelId && len(hook.TriggerWords) == 0 {
				relevantHooks = append(relevantHooks, hook)
			} else if hook.TriggerWhen == TRIGGERWORDS_FULL && hook.HasTriggerWord(firstWord) {
				relevantHooks = append(relevantHooks, hook)
			} else if hook.TriggerWhen == TRIGGERWORDS_STARTSWITH && hook.TriggerWordStartsWith(firstWord) {
				relevantHooks = append(relevantHooks, hook)
			}
		}
	}

	for _, hook := range relevantHooks {
		go func(hook *model.OutgoingWebhook) {
			payload := &model.OutgoingWebhookPayload{
				Token:       hook.Token,
				TeamId:      hook.TeamId,
				TeamDomain:  team.Name,
				ChannelId:   post.ChannelId,
				ChannelName: channel.Name,
				Timestamp:   post.CreateAt,
				UserId:      post.UserId,
				UserName:    user.Username,
				PostId:      post.Id,
				Text:        post.Message,
				TriggerWord: firstWord,
			}
			var body io.Reader
			var contentType string
			if hook.ContentType == "application/json" {
				body = strings.NewReader(payload.ToJSON())
				contentType = "application/json"
			} else {
				body = strings.NewReader(payload.ToFormValues())
				contentType = "application/x-www-form-urlencoded"
			}
			tr := &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: *utils.Cfg.ServiceSettings.EnableInsecureOutgoingConnections},
			}
			client := &http.Client{Transport: tr}

			for _, url := range hook.CallbackURLs {
				go func(url string) {
					req, _ := http.NewRequest("POST", url, body)
					req.Header.Set("Content-Type", contentType)
					req.Header.Set("Accept", "application/json")
					if resp, err := client.Do(req); err != nil {
						l4g.Error(utils.T("api.post.handle_webhook_events_and_forget.event_post.error"), err.Error())
					} else {
						defer func() {
							ioutil.ReadAll(resp.Body)
							resp.Body.Close()
						}()
						respProps := model.MapFromJson(resp.Body)

						if text, ok := respProps["text"]; ok {
							if _, err := CreateWebhookPost(hook.CreatorId, hook.TeamId, post.ChannelId, text, respProps["username"], respProps["icon_url"], post.Props, post.Type); err != nil {
								l4g.Error(utils.T("api.post.handle_webhook_events_and_forget.create_post.error"), err)
							}
						}
					}
				}(url)
			}

		}(hook)
	}

	return nil
}

func CreateWebhookPost(userId, teamId, channelId, text, overrideUsername, overrideIconUrl string, props model.StringInterface, postType string) (*model.Post, *model.AppError) {
	// parse links into Markdown format
	linkWithTextRegex := regexp.MustCompile(`<([^<\|]+)\|([^>]+)>`)
	text = linkWithTextRegex.ReplaceAllString(text, "[${2}](${1})")

	post := &model.Post{UserId: userId, ChannelId: channelId, Message: text, Type: postType}
	post.AddProp("from_webhook", "true")

	if metrics := einterfaces.GetMetricsInterface(); metrics != nil {
		metrics.IncrementWebhookPost()
	}

	if utils.Cfg.ServiceSettings.EnablePostUsernameOverride {
		if len(overrideUsername) != 0 {
			post.AddProp("override_username", overrideUsername)
		} else {
			post.AddProp("override_username", model.DEFAULT_WEBHOOK_USERNAME)
		}
	}

	if utils.Cfg.ServiceSettings.EnablePostIconOverride {
		if len(overrideIconUrl) != 0 {
			post.AddProp("override_icon_url", overrideIconUrl)
		}
	}

	if len(props) > 0 {
		for key, val := range props {
			if key == "attachments" {
				if attachments, success := val.([]*model.SlackAttachment); success {
					parseSlackAttachment(post, attachments)
				}
			} else if key != "override_icon_url" && key != "override_username" && key != "from_webhook" {
				post.AddProp(key, val)
			}
		}
	}

	if _, err := CreatePost(post, teamId, false); err != nil {
		return nil, model.NewLocAppError("CreateWebhookPost", "api.post.create_webhook_post.creating.app_error", nil, "err="+err.Message)
	}

	return post, nil
}

func CreateIncomingWebhookForChannel(creatorId string, channel *model.Channel, hook *model.IncomingWebhook) (*model.IncomingWebhook, *model.AppError) {
	if !utils.Cfg.ServiceSettings.EnableIncomingWebhooks {
		return nil, model.NewAppError("CreateIncomingWebhookForChannel", "api.incoming_webhook.disabled.app_error", nil, "", http.StatusNotImplemented)
	}

	hook.UserId = creatorId
	hook.TeamId = channel.TeamId

	if result := <-Srv.Store.Webhook().SaveIncoming(hook); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.(*model.IncomingWebhook), nil
	}
}

func UpdateIncomingWebhook(oldHook, updatedHook *model.IncomingWebhook) (*model.IncomingWebhook, *model.AppError) {
	if !utils.Cfg.ServiceSettings.EnableIncomingWebhooks {
		return nil, model.NewAppError("UpdateIncomingWebhook", "api.incoming_webhook.disabled.app_error", nil, "", http.StatusNotImplemented)
	}

	updatedHook.Id = oldHook.Id
	updatedHook.UserId = oldHook.UserId
	updatedHook.CreateAt = oldHook.CreateAt
	updatedHook.UpdateAt = model.GetMillis()
	updatedHook.TeamId = oldHook.TeamId
	updatedHook.DeleteAt = oldHook.DeleteAt

	if result := <-Srv.Store.Webhook().UpdateIncoming(updatedHook); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.(*model.IncomingWebhook), nil
	}
}

func DeleteIncomingWebhook(hookId string) *model.AppError {
	if !utils.Cfg.ServiceSettings.EnableIncomingWebhooks {
		return model.NewAppError("DeleteIncomingWebhook", "api.incoming_webhook.disabled.app_error", nil, "", http.StatusNotImplemented)
	}

	if result := <-Srv.Store.Webhook().DeleteIncoming(hookId, model.GetMillis()); result.Err != nil {
		return result.Err
	}

	InvalidateCacheForWebhook(hookId)

	return nil
}

func GetIncomingWebhook(hookId string) (*model.IncomingWebhook, *model.AppError) {
	if !utils.Cfg.ServiceSettings.EnableIncomingWebhooks {
		return nil, model.NewAppError("GetIncomingWebhook", "api.incoming_webhook.disabled.app_error", nil, "", http.StatusNotImplemented)
	}

	if result := <-Srv.Store.Webhook().GetIncoming(hookId, true); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.(*model.IncomingWebhook), nil
	}
}

func GetIncomingWebhooksForTeamPage(teamId string, page, perPage int) ([]*model.IncomingWebhook, *model.AppError) {
	if !utils.Cfg.ServiceSettings.EnableIncomingWebhooks {
		return nil, model.NewAppError("GetIncomingWebhooksForTeamPage", "api.incoming_webhook.disabled.app_error", nil, "", http.StatusNotImplemented)
	}

	if result := <-Srv.Store.Webhook().GetIncomingByTeam(teamId, page*perPage, perPage); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.([]*model.IncomingWebhook), nil
	}
}

func GetIncomingWebhooksPage(page, perPage int) ([]*model.IncomingWebhook, *model.AppError) {
	if !utils.Cfg.ServiceSettings.EnableIncomingWebhooks {
		return nil, model.NewAppError("GetIncomingWebhooksPage", "api.incoming_webhook.disabled.app_error", nil, "", http.StatusNotImplemented)
	}

	if result := <-Srv.Store.Webhook().GetIncomingList(page*perPage, perPage); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.([]*model.IncomingWebhook), nil
	}
}

func CreateOutgoingWebhook(hook *model.OutgoingWebhook) (*model.OutgoingWebhook, *model.AppError) {
	if !utils.Cfg.ServiceSettings.EnableOutgoingWebhooks {
		return nil, model.NewAppError("CreateOutgoingWebhook", "api.outgoing_webhook.disabled.app_error", nil, "", http.StatusNotImplemented)
	}

	if len(hook.ChannelId) != 0 {
		cchan := Srv.Store.Channel().Get(hook.ChannelId, true)

		var channel *model.Channel
		if result := <-cchan; result.Err != nil {
			return nil, result.Err
		} else {
			channel = result.Data.(*model.Channel)
		}

		if channel.Type != model.CHANNEL_OPEN {
			return nil, model.NewAppError("CreateOutgoingWebhook", "api.outgoing_webhook.disabled.app_error", nil, "", http.StatusForbidden)
		}

		if channel.Type != model.CHANNEL_OPEN || channel.TeamId != hook.TeamId {
			return nil, model.NewAppError("CreateOutgoingWebhook", "api.webhook.create_outgoing.permissions.app_error", nil, "", http.StatusForbidden)
		}
	} else if len(hook.TriggerWords) == 0 {
		return nil, model.NewAppError("CreateOutgoingWebhook", "api.webhook.create_outgoing.triggers.app_error", nil, "", http.StatusBadRequest)
	}

	if result := <-Srv.Store.Webhook().GetOutgoingByTeam(hook.TeamId, -1, -1); result.Err != nil {
		return nil, result.Err
	} else {
		allHooks := result.Data.([]*model.OutgoingWebhook)

		for _, existingOutHook := range allHooks {
			urlIntersect := utils.StringArrayIntersection(existingOutHook.CallbackURLs, hook.CallbackURLs)
			triggerIntersect := utils.StringArrayIntersection(existingOutHook.TriggerWords, hook.TriggerWords)

			if existingOutHook.ChannelId == hook.ChannelId && len(urlIntersect) != 0 && len(triggerIntersect) != 0 {
				return nil, model.NewLocAppError("CreateOutgoingWebhook", "api.webhook.create_outgoing.intersect.app_error", nil, "")
			}
		}
	}

	if result := <-Srv.Store.Webhook().SaveOutgoing(hook); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.(*model.OutgoingWebhook), nil
	}
}

func UpdateOutgoingWebhook(oldHook, updatedHook *model.OutgoingWebhook) (*model.OutgoingWebhook, *model.AppError) {
	if !utils.Cfg.ServiceSettings.EnableOutgoingWebhooks {
		return nil, model.NewAppError("UpdateOutgoingWebhook", "api.outgoing_webhook.disabled.app_error", nil, "", http.StatusNotImplemented)
	}

	if len(updatedHook.ChannelId) > 0 {
		channel, err := GetChannel(updatedHook.ChannelId)
		if err != nil {
			return nil, err
		}

		if channel.Type != model.CHANNEL_OPEN {
			return nil, model.NewAppError("UpdateOutgoingWebhook", "api.webhook.create_outgoing.not_open.app_error", nil, "", http.StatusForbidden)
		}

		if channel.TeamId != oldHook.TeamId {
			return nil, model.NewAppError("UpdateOutgoingWebhook", "api.webhook.create_outgoing.permissions.app_error", nil, "", http.StatusForbidden)
		}
	} else if len(updatedHook.TriggerWords) == 0 {
		return nil, model.NewLocAppError("UpdateOutgoingWebhook", "api.webhook.create_outgoing.triggers.app_error", nil, "")
	}

	var result store.StoreResult
	if result = <-Srv.Store.Webhook().GetOutgoingByTeam(oldHook.TeamId, -1, -1); result.Err != nil {
		return nil, result.Err
	}

	allHooks := result.Data.([]*model.OutgoingWebhook)

	for _, existingOutHook := range allHooks {
		urlIntersect := utils.StringArrayIntersection(existingOutHook.CallbackURLs, updatedHook.CallbackURLs)
		triggerIntersect := utils.StringArrayIntersection(existingOutHook.TriggerWords, updatedHook.TriggerWords)

		if existingOutHook.ChannelId == updatedHook.ChannelId && len(urlIntersect) != 0 && len(triggerIntersect) != 0 && existingOutHook.Id != updatedHook.Id {
			return nil, model.NewAppError("UpdateOutgoingWebhook", "api.webhook.update_outgoing.intersect.app_error", nil, "", http.StatusBadRequest)
		}
	}

	updatedHook.CreatorId = oldHook.CreatorId
	updatedHook.CreateAt = oldHook.CreateAt
	updatedHook.DeleteAt = oldHook.DeleteAt
	updatedHook.TeamId = oldHook.TeamId
	updatedHook.UpdateAt = model.GetMillis()

	if result = <-Srv.Store.Webhook().UpdateOutgoing(updatedHook); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.(*model.OutgoingWebhook), nil
	}
}

func GetOutgoingWebhook(hookId string) (*model.OutgoingWebhook, *model.AppError) {
	if !utils.Cfg.ServiceSettings.EnableOutgoingWebhooks {
		return nil, model.NewAppError("GetOutgoingWebhook", "api.outgoing_webhook.disabled.app_error", nil, "", http.StatusNotImplemented)
	}

	if result := <-Srv.Store.Webhook().GetOutgoing(hookId); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.(*model.OutgoingWebhook), nil
	}
}

func GetOutgoingWebhooksPage(page, perPage int) ([]*model.OutgoingWebhook, *model.AppError) {
	if !utils.Cfg.ServiceSettings.EnableOutgoingWebhooks {
		return nil, model.NewAppError("GetOutgoingWebhooksPage", "api.outgoing_webhook.disabled.app_error", nil, "", http.StatusNotImplemented)
	}

	if result := <-Srv.Store.Webhook().GetOutgoingList(page*perPage, perPage); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.([]*model.OutgoingWebhook), nil
	}
}

func GetOutgoingWebhooksForChannelPage(channelId string, page, perPage int) ([]*model.OutgoingWebhook, *model.AppError) {
	if !utils.Cfg.ServiceSettings.EnableOutgoingWebhooks {
		return nil, model.NewAppError("GetOutgoingWebhooksForChannelPage", "api.outgoing_webhook.disabled.app_error", nil, "", http.StatusNotImplemented)
	}

	if result := <-Srv.Store.Webhook().GetOutgoingByChannel(channelId, page*perPage, perPage); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.([]*model.OutgoingWebhook), nil
	}
}

func GetOutgoingWebhooksForTeamPage(teamId string, page, perPage int) ([]*model.OutgoingWebhook, *model.AppError) {
	if !utils.Cfg.ServiceSettings.EnableOutgoingWebhooks {
		return nil, model.NewAppError("GetOutgoingWebhooksForTeamPage", "api.outgoing_webhook.disabled.app_error", nil, "", http.StatusNotImplemented)
	}

	if result := <-Srv.Store.Webhook().GetOutgoingByTeam(teamId, page*perPage, perPage); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.([]*model.OutgoingWebhook), nil
	}
}

func DeleteOutgoingWebhook(hookId string) *model.AppError {
	if !utils.Cfg.ServiceSettings.EnableOutgoingWebhooks {
		return model.NewAppError("DeleteOutgoingWebhook", "api.outgoing_webhook.disabled.app_error", nil, "", http.StatusNotImplemented)
	}

	if result := <-Srv.Store.Webhook().DeleteOutgoing(hookId, model.GetMillis()); result.Err != nil {
		return result.Err
	}

	return nil
}

func RegenOutgoingWebhookToken(hook *model.OutgoingWebhook) (*model.OutgoingWebhook, *model.AppError) {
	if !utils.Cfg.ServiceSettings.EnableOutgoingWebhooks {
		return nil, model.NewAppError("RegenOutgoingWebhookToken", "api.outgoing_webhook.disabled.app_error", nil, "", http.StatusNotImplemented)
	}

	hook.Token = model.NewId()

	if result := <-Srv.Store.Webhook().UpdateOutgoing(hook); result.Err != nil {
		return nil, result.Err
	} else {
		return result.Data.(*model.OutgoingWebhook), nil
	}
}

func HandleIncomingWebhook(hookId string, req *model.IncomingWebhookRequest) *model.AppError {
	if !utils.Cfg.ServiceSettings.EnableIncomingWebhooks {
		return model.NewAppError("HandleIncomingWebhook", "web.incoming_webhook.disabled.app_error", nil, "", http.StatusNotImplemented)
	}

	hchan := Srv.Store.Webhook().GetIncoming(hookId, true)

	if req == nil {
		return model.NewAppError("HandleIncomingWebhook", "web.incoming_webhook.parse.app_error", nil, "", http.StatusBadRequest)
	}

	text := req.Text
	if len(text) == 0 && req.Attachments == nil {
		return model.NewAppError("HandleIncomingWebhook", "web.incoming_webhook.text.app_error", nil, "", http.StatusBadRequest)
	}

	textSize := utf8.RuneCountInString(text)
	if textSize > model.POST_MESSAGE_MAX_RUNES {
		return model.NewAppError("HandleIncomingWebhook", "web.incoming_webhook.text.length.app_error", map[string]interface{}{"Max": model.POST_MESSAGE_MAX_RUNES, "Actual": textSize}, "", http.StatusBadRequest)
	}

	channelName := req.ChannelName
	webhookType := req.Type

	// attachments is in here for slack compatibility
	if len(req.Attachments) > 0 {
		if len(req.Props) == 0 {
			req.Props = make(model.StringInterface)
		}
		req.Props["attachments"] = req.Attachments

		attachmentSize := utf8.RuneCountInString(model.StringInterfaceToJson(req.Props))
		// Minus 100 to leave room for setting post type in the Props
		if attachmentSize > model.POST_PROPS_MAX_RUNES-100 {
			return model.NewAppError("HandleIncomingWebhook", "web.incoming_webhook.attachment.app_error", map[string]interface{}{"Max": model.POST_PROPS_MAX_RUNES - 100, "Actual": attachmentSize}, "", http.StatusBadRequest)
		}

		webhookType = model.POST_SLACK_ATTACHMENT
	}

	var hook *model.IncomingWebhook
	if result := <-hchan; result.Err != nil {
		return model.NewAppError("HandleIncomingWebhook", "web.incoming_webhook.invalid.app_error", nil, "err="+result.Err.Message, http.StatusBadRequest)
	} else {
		hook = result.Data.(*model.IncomingWebhook)
	}

	var channel *model.Channel
	var cchan store.StoreChannel
	var directUserId string

	if len(channelName) != 0 {
		if channelName[0] == '@' {
			if result := <-Srv.Store.User().GetByUsername(channelName[1:]); result.Err != nil {
				return model.NewAppError("HandleIncomingWebhook", "web.incoming_webhook.user.app_error", nil, "err="+result.Err.Message, http.StatusBadRequest)
			} else {
				directUserId = result.Data.(*model.User).Id
				channelName = model.GetDMNameFromIds(directUserId, hook.UserId)
			}
		} else if channelName[0] == '#' {
			channelName = channelName[1:]
		}

		cchan = Srv.Store.Channel().GetByName(hook.TeamId, channelName, true)
	} else {
		cchan = Srv.Store.Channel().Get(hook.ChannelId, true)
	}

	overrideUsername := req.Username
	overrideIconUrl := req.IconURL

	result := <-cchan
	if result.Err != nil && result.Err.Id == store.MISSING_CHANNEL_ERROR && directUserId != "" {
		newChanResult := <-Srv.Store.Channel().CreateDirectChannel(directUserId, hook.UserId)
		if newChanResult.Err != nil {
			return model.NewAppError("HandleIncomingWebhook", "web.incoming_webhook.channel.app_error", nil, "err="+newChanResult.Err.Message, http.StatusBadRequest)
		} else {
			channel = newChanResult.Data.(*model.Channel)
			InvalidateCacheForUser(directUserId)
			InvalidateCacheForUser(hook.UserId)
		}
	} else if result.Err != nil {
		return model.NewAppError("HandleIncomingWebhook", "web.incoming_webhook.channel.app_error", nil, "err="+result.Err.Message, result.Err.StatusCode)
	} else {
		channel = result.Data.(*model.Channel)
	}

	if channel.Type != model.CHANNEL_OPEN && !HasPermissionToChannel(hook.UserId, channel.Id, model.PERMISSION_READ_CHANNEL) {
		return model.NewAppError("HandleIncomingWebhook", "web.incoming_webhook.permissions.app_error", nil, "", http.StatusForbidden)
	}

	if _, err := CreateWebhookPost(hook.UserId, hook.TeamId, channel.Id, text, overrideUsername, overrideIconUrl, req.Props, webhookType); err != nil {
		return err
	}

	return nil
}
