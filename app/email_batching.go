// Copyright (c) 2016-present Mattermost, Inc. All Rights Reserved.
// See License.txt for license information.

package app

import (
	"database/sql"
	"fmt"
	"html/template"
	"strconv"
	"time"

	"github.com/primefour/servers/model"
	"github.com/primefour/servers/utils"

	l4g "github.com/alecthomas/log4go"
	"github.com/nicksnyder/go-i18n/i18n"
)

const (
	EMAIL_BATCHING_TASK_NAME = "Email Batching"
)

var emailBatchingJob *EmailBatchingJob

func InitEmailBatching() {
	if *utils.Cfg.EmailSettings.EnableEmailBatching {
		if emailBatchingJob == nil {
			emailBatchingJob = MakeEmailBatchingJob(*utils.Cfg.EmailSettings.EmailBatchingBufferSize)
		}

		// note that we don't support changing EmailBatchingBufferSize without restarting the server

		emailBatchingJob.Start()
	}
}

func AddNotificationEmailToBatch(user *model.User, post *model.Post, team *model.Team) *model.AppError {
	if !*utils.Cfg.EmailSettings.EnableEmailBatching {
		return model.NewLocAppError("AddNotificationEmailToBatch", "api.email_batching.add_notification_email_to_batch.disabled.app_error", nil, "")
	}

	if !emailBatchingJob.Add(user, post, team) {
		l4g.Error(utils.T("api.email_batching.add_notification_email_to_batch.channel_full.app_error"))
		return model.NewLocAppError("AddNotificationEmailToBatch", "api.email_batching.add_notification_email_to_batch.channel_full.app_error", nil, "")
	}

	return nil
}

type batchedNotification struct {
	userId   string
	post     *model.Post
	teamName string
}

type EmailBatchingJob struct {
	newNotifications     chan *batchedNotification
	pendingNotifications map[string][]*batchedNotification
}

func MakeEmailBatchingJob(bufferSize int) *EmailBatchingJob {
	return &EmailBatchingJob{
		newNotifications:     make(chan *batchedNotification, bufferSize),
		pendingNotifications: make(map[string][]*batchedNotification),
	}
}

func (job *EmailBatchingJob) Start() {
	if task := model.GetTaskByName(EMAIL_BATCHING_TASK_NAME); task != nil {
		task.Cancel()
	}

	l4g.Debug(utils.T("api.email_batching.start.starting"), *utils.Cfg.EmailSettings.EmailBatchingInterval)
	model.CreateRecurringTask(EMAIL_BATCHING_TASK_NAME, job.CheckPendingEmails, time.Duration(*utils.Cfg.EmailSettings.EmailBatchingInterval)*time.Second)
}

func (job *EmailBatchingJob) Add(user *model.User, post *model.Post, team *model.Team) bool {
	notification := &batchedNotification{
		userId:   user.Id,
		post:     post,
		teamName: team.Name,
	}

	select {
	case job.newNotifications <- notification:
		return true
	default:
		// return false if we couldn't queue the email notification so that we can send an immediate email
		return false
	}
}

func (job *EmailBatchingJob) CheckPendingEmails() {
	job.handleNewNotifications()

	// it's a bit weird to pass the send email function through here, but it makes it so that we can test
	// without actually sending emails
	job.checkPendingNotifications(time.Now(), sendBatchedEmailNotification)

	l4g.Debug(utils.T("api.email_batching.check_pending_emails.finished_running"), len(job.pendingNotifications))
}

func (job *EmailBatchingJob) handleNewNotifications() {
	receiving := true

	// read in new notifications to send
	for receiving {
		select {
		case notification := <-job.newNotifications:
			userId := notification.userId

			if _, ok := job.pendingNotifications[userId]; !ok {
				job.pendingNotifications[userId] = []*batchedNotification{notification}
			} else {
				job.pendingNotifications[userId] = append(job.pendingNotifications[userId], notification)
			}
		default:
			receiving = false
		}
	}
}

func (job *EmailBatchingJob) checkPendingNotifications(now time.Time, handler func(string, []*batchedNotification)) {
	// look for users who've acted since pending posts were received
	for userId, notifications := range job.pendingNotifications {
		schan := Srv.Store.Status().Get(userId)
		pchan := Srv.Store.Preference().Get(userId, model.PREFERENCE_CATEGORY_NOTIFICATIONS, model.PREFERENCE_NAME_EMAIL_INTERVAL)
		batchStartTime := notifications[0].post.CreateAt

		// check if the user has been active and would've seen any new posts
		if result := <-schan; result.Err != nil {
			l4g.Error(utils.T("api.email_batching.check_pending_emails.status.app_error"), result.Err)
			delete(job.pendingNotifications, userId)
			continue
		} else if status := result.Data.(*model.Status); status.LastActivityAt >= batchStartTime {
			delete(job.pendingNotifications, userId)
			continue
		}

		// get how long we need to wait to send notifications to the user
		var interval int64
		if result := <-pchan; result.Err != nil {
			// default to 30 seconds to match the send "immediate" setting
			interval, _ = strconv.ParseInt(model.PREFERENCE_DEFAULT_EMAIL_INTERVAL, 10, 64)
		} else {
			preference := result.Data.(model.Preference)

			if value, err := strconv.ParseInt(preference.Value, 10, 64); err != nil {
				interval, _ = strconv.ParseInt(model.PREFERENCE_DEFAULT_EMAIL_INTERVAL, 10, 64)
			} else {
				interval = value
			}
		}

		// send the email notification if it's been long enough
		if now.Sub(time.Unix(batchStartTime/1000, 0)) > time.Duration(interval)*time.Second {
			go handler(userId, notifications)
			delete(job.pendingNotifications, userId)
		}
	}
}

func sendBatchedEmailNotification(userId string, notifications []*batchedNotification) {
	uchan := Srv.Store.User().Get(userId)
	pchan := Srv.Store.Preference().Get(userId, model.PREFERENCE_CATEGORY_DISPLAY_SETTINGS, model.PREFERENCE_NAME_DISPLAY_NAME_FORMAT)

	var user *model.User
	if result := <-uchan; result.Err != nil {
		l4g.Warn("api.email_batching.send_batched_email_notification.user.app_error")
		return
	} else {
		user = result.Data.(*model.User)
	}

	translateFunc := utils.GetUserTranslations(user.Locale)

	var displayNameFormat string
	if result := <-pchan; result.Err != nil && result.Err.DetailedError != sql.ErrNoRows.Error() {
		l4g.Warn("api.email_batching.send_batched_email_notification.preferences.app_error")
		return
	} else if result.Err != nil {
		// no display name format saved, so fall back to default
		displayNameFormat = model.PREFERENCE_DEFAULT_DISPLAY_NAME_FORMAT
	} else {
		displayNameFormat = result.Data.(model.Preference).Value
	}

	var contents string
	for _, notification := range notifications {
		template := utils.NewHTMLTemplate("post_batched_post", user.Locale)

		contents += renderBatchedPost(template, notification.post, notification.teamName, displayNameFormat, translateFunc)
	}

	tm := time.Unix(notifications[0].post.CreateAt/1000, 0)

	subject := translateFunc("api.email_batching.send_batched_email_notification.subject", len(notifications), map[string]interface{}{
		"SiteName": utils.Cfg.TeamSettings.SiteName,
		"Year":     tm.Year(),
		"Month":    translateFunc(tm.Month().String()),
		"Day":      tm.Day(),
	})

	body := utils.NewHTMLTemplate("post_batched_body", user.Locale)
	body.Props["SiteURL"] = *utils.Cfg.ServiceSettings.SiteURL
	body.Props["Posts"] = template.HTML(contents)
	body.Props["BodyText"] = translateFunc("api.email_batching.send_batched_email_notification.body_text", len(notifications))

	if err := utils.SendMail(user.Email, subject, body.Render()); err != nil {
		l4g.Warn(utils.T("api.email_batching.send_batched_email_notification.send.app_error"), user.Email, err)
	}
}

func renderBatchedPost(template *utils.HTMLTemplate, post *model.Post, teamName string, displayNameFormat string, translateFunc i18n.TranslateFunc) string {
	schan := Srv.Store.User().Get(post.UserId)
	cchan := Srv.Store.Channel().Get(post.ChannelId, true)

	template.Props["Button"] = translateFunc("api.email_batching.render_batched_post.go_to_post")
	template.Props["PostMessage"] = GetMessageForNotification(post, translateFunc)
	template.Props["PostLink"] = *utils.Cfg.ServiceSettings.SiteURL + "/" + teamName + "/pl/" + post.Id

	tm := time.Unix(post.CreateAt/1000, 0)
	timezone, _ := tm.Zone()

	template.Props["Date"] = translateFunc("api.email_batching.render_batched_post.date", map[string]interface{}{
		"Year":     tm.Year(),
		"Month":    translateFunc(tm.Month().String()),
		"Day":      tm.Day(),
		"Hour":     tm.Hour(),
		"Minute":   fmt.Sprintf("%02d", tm.Minute()),
		"Timezone": timezone,
	})

	if result := <-schan; result.Err != nil {
		l4g.Warn(utils.T("api.email_batching.render_batched_post.sender.app_error"))
		return ""
	} else {
		template.Props["SenderName"] = result.Data.(*model.User).GetDisplayNameForPreference(displayNameFormat)
	}

	if result := <-cchan; result.Err != nil {
		l4g.Warn(utils.T("api.email_batching.render_batched_post.channel.app_error"))
		return ""
	} else if channel := result.Data.(*model.Channel); channel.Type == model.CHANNEL_DIRECT {
		template.Props["ChannelName"] = translateFunc("api.email_batching.render_batched_post.direct_message")
	} else if channel.Type == model.CHANNEL_GROUP {
		template.Props["ChannelName"] = translateFunc("api.email_batching.render_batched_post.group_message")
	} else {
		template.Props["ChannelName"] = channel.DisplayName
	}

	return template.Render()
}
