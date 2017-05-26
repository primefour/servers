// Copyright (c) 2017-present Mattermost, Inc. All Rights Reserved.
// See License.txt for license information.

package app

import (
	"crypto/tls"
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/primefour/servers/model"
	"github.com/primefour/servers/utils"
)

func RevokeWebrtcToken(sessionId string) {
	token := base64.StdEncoding.EncodeToString([]byte(sessionId))
	data := make(map[string]string)
	data["janus"] = "remove_token"
	data["token"] = token
	data["transaction"] = model.NewId()
	data["admin_secret"] = *utils.Cfg.WebrtcSettings.GatewayAdminSecret

	rq, _ := http.NewRequest("POST", *utils.Cfg.WebrtcSettings.GatewayAdminUrl, strings.NewReader(model.MapToJson(data)))
	rq.Header.Set("Content-Type", "application/json")

	// we do not care about the response
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: *utils.Cfg.ServiceSettings.EnableInsecureOutgoingConnections},
	}
	httpClient := &http.Client{Transport: tr}
	httpClient.Do(rq)
}
