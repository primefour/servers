// Copyright (c) 2017-present Mattermost, Inc. All Rights Reserved.
// See License.txt for license information.

package wsapi

import (
	"github.com/primefour/servers/app"
)

func InitRouter() {
	app.Srv.WebSocketRouter = app.NewWebSocketRouter()
}

func InitApi() {
	InitUser()
	InitSystem()
	InitStatus()
	InitWebrtc()

	app.HubStart()
}
