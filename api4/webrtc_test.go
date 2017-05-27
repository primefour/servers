// Copyright (c) 2017 Mattermost, Inc. All Rights Reserved.
// See License.txt for license information.

package api4

import (
	"testing"

	"github.com/primefour/servers/utils"
)

func TestGetWebrtcToken(t *testing.T) {
	th := Setup().InitBasic().InitSystemAdmin()
	defer TearDown()
	Client := th.Client

	enableWebrtc := *utils.Cfg.WebrtcSettings.Enable
	defer func() {
		*utils.Cfg.WebrtcSettings.Enable = enableWebrtc
	}()
	*utils.Cfg.WebrtcSettings.Enable = false

	_, resp := Client.GetWebrtcToken()
	CheckNotImplementedStatus(t, resp)

	Client.Logout()
	_, resp = Client.GetWebrtcToken()
	CheckUnauthorizedStatus(t, resp)
}
