// Copyright (c) 2016-present Mattermost, Inc. All Rights Reserved.
// See License.txt for license information.

package app

import (
	"github.com/primefour/servers/model"
	goi18n "github.com/nicksnyder/go-i18n/i18n"
)

type OnlineProvider struct {
}

const (
	CMD_ONLINE = "online"
)

func init() {
	RegisterCommandProvider(&OnlineProvider{})
}

func (me *OnlineProvider) GetTrigger() string {
	return CMD_ONLINE
}

func (me *OnlineProvider) GetCommand(T goi18n.TranslateFunc) *model.Command {
	return &model.Command{
		Trigger:          CMD_ONLINE,
		AutoComplete:     true,
		AutoCompleteDesc: T("api.command_online.desc"),
		DisplayName:      T("api.command_online.name"),
	}
}

func (me *OnlineProvider) DoCommand(args *model.CommandArgs, message string) *model.CommandResponse {
	rmsg := args.T("api.command_online.success")
	if len(message) > 0 {
		rmsg = message + " " + rmsg
	}
	SetStatusOnline(args.UserId, args.Session.Id, true)

	return &model.CommandResponse{ResponseType: model.COMMAND_RESPONSE_TYPE_EPHEMERAL, Text: rmsg}
}
