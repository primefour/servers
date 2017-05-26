// Copyright (c) 2016-present Mattermost, Inc. All Rights Reserved.
// See License.txt for license information.

package model

import (
	"encoding/json"
	"fmt"
	"io"
)

const (
	COMMAND_RESPONSE_TYPE_IN_CHANNEL = "in_channel"
	COMMAND_RESPONSE_TYPE_EPHEMERAL  = "ephemeral"
)

type CommandResponse struct {
	ResponseType string             `json:"response_type"`
	Text         string             `json:"text"`
	Username     string             `json:"username"`
	IconURL      string             `json:"icon_url"`
	GotoLocation string             `json:"goto_location"`
	Attachments  []*SlackAttachment `json:"attachments"`
}

func (o *CommandResponse) ToJson() string {
	b, err := json.Marshal(o)
	if err != nil {
		return ""
	} else {
		return string(b)
	}
}

func CommandResponseFromJson(data io.Reader) *CommandResponse {
	decoder := json.NewDecoder(data)
	var o CommandResponse

	if err := decoder.Decode(&o); err != nil {
		return nil
	}

	// Ensure attachment fields are stored as strings
	for _, attachment := range o.Attachments {
		for _, field := range attachment.Fields {
			if field.Value != nil {
				field.Value = fmt.Sprintf("%v", field.Value)
			}
		}
	}

	return &o
}
