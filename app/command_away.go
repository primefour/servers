// Copyright (c) 2016-present Mattermost, Inc. All Rights Reserved.
// See License.txt for license information.

package app

import (
	"github.com/primefour/servers/model"
	goi18n "github.com/nicksnyder/go-i18n/i18n"
)

type AwayProvider struct {
}

const (
	CMD_AWAY = "away"
)

func init() {
	RegisterCommandProvider(&AwayProvider{})
}

func (me *AwayProvider) GetTrigger() string {
	return CMD_AWAY
}

func (me *AwayProvider) GetCommand(T goi18n.TranslateFunc) *model.Command {
	return &model.Command{
		Trigger:          CMD_AWAY,
		AutoComplete:     true,
		AutoCompleteDesc: T("api.command_away.desc"),
		DisplayName:      T("api.command_away.name"),
	}
}

func (me *AwayProvider) DoCommand(args *model.CommandArgs, message string) *model.CommandResponse {
	rmsg := args.T("api.command_away.success")
	if len(message) > 0 {
		rmsg = message + " " + rmsg
	}
	SetStatusAwayIfNeeded(args.UserId, true)

	return &model.CommandResponse{ResponseType: model.COMMAND_RESPONSE_TYPE_EPHEMERAL, Text: rmsg}
}
