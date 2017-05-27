// Copyright (c) 2016-present Mattermost, Inc. All Rights Reserved.
// See License.txt for license information.

package main

import (
	"os"
	"os/signal"
	"syscall"
	"time"

	l4g "github.com/alecthomas/log4go"
	"github.com/primefour/servers/api4"
	"github.com/primefour/servers/app"
	"github.com/primefour/servers/einterfaces"
	"github.com/primefour/servers/manualtesting"
	"github.com/primefour/servers/model"
	"github.com/primefour/servers/utils"
	"github.com/primefour/servers/web"
	"github.com/primefour/servers/wsapi"
	"github.com/spf13/cobra"
)

var MaxNotificationsPerChannelDefault int64 = 1000000

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Run the Mattermost server",
	RunE:  runServerCmd,
}

func runServerCmd(cmd *cobra.Command, args []string) error {
	config, err := cmd.Flags().GetString("config")
	if err != nil {
		return err
	}

	utils.CfgDisableConfigWatch, _ = cmd.Flags().GetBool("disableconfigwatch")

	runServer(config)
	return nil
}

func runServer(configFileLocation string) {
	if errstr := doLoadConfig(configFileLocation); errstr != "" {
		l4g.Exit("Unable to load mattermost configuration file: ", errstr)
		return
	}

	utils.InitTranslations(utils.Cfg.LocalizationSettings)
	utils.TestConnection(utils.Cfg)

	pwd, _ := os.Getwd()
	l4g.Info(utils.T("mattermost.current_version"), model.CurrentVersion, model.BuildNumber, model.BuildDate, model.BuildHash, model.BuildHashEnterprise)
	l4g.Info(utils.T("mattermost.entreprise_enabled"), model.BuildEnterpriseReady)
	l4g.Info(utils.T("mattermost.working_dir"), pwd)
	l4g.Info(utils.T("mattermost.config_file"), utils.FindConfigFile(configFileLocation))

	// Enable developer settings if this is a "dev" build
	if model.BuildNumber == "dev" {
		*utils.Cfg.ServiceSettings.EnableDeveloper = true
	}

	app.NewServer()
	app.InitStores()
	api4.InitRouter()
	wsapi.InitRouter()
	api4.InitApi(false)
	wsapi.InitApi()
	web.InitWeb()

	if model.BuildEnterpriseReady == "true" {
		app.LoadLicense()
	}

	if !utils.IsLicensed && len(utils.Cfg.SqlSettings.DataSourceReplicas) > 1 {
		l4g.Warn(utils.T("store.sql.read_replicas_not_licensed.critical"))
		utils.Cfg.SqlSettings.DataSourceReplicas = utils.Cfg.SqlSettings.DataSourceReplicas[:1]
	}

	if !utils.IsLicensed {
		utils.Cfg.TeamSettings.MaxNotificationsPerChannel = &MaxNotificationsPerChannelDefault
	}

	app.ReloadConfig()

	resetStatuses()

	app.StartServer()

	// If we allow testing then listen for manual testing URL hits
	if utils.Cfg.ServiceSettings.EnableTesting {
		manualtesting.InitManualTesting()
	}

	setDiagnosticId()
	utils.RegenerateClientConfig()
	go runSecurityJob()
	go runDiagnosticsJob()

	go runTokenCleanupJob()

	if complianceI := einterfaces.GetComplianceInterface(); complianceI != nil {
		complianceI.StartComplianceDailyJob()
	}

	if einterfaces.GetClusterInterface() != nil {
		einterfaces.GetClusterInterface().StartInterNodeCommunication()
	}

	if einterfaces.GetMetricsInterface() != nil {
		einterfaces.GetMetricsInterface().StartServer()
	}

	// wait for kill signal before attempting to gracefully shutdown
	// the running service
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-c

	if einterfaces.GetClusterInterface() != nil {
		einterfaces.GetClusterInterface().StopInterNodeCommunication()
	}

	if einterfaces.GetMetricsInterface() != nil {
		einterfaces.GetMetricsInterface().StopServer()
	}

	app.StopServer()
}

func runSecurityJob() {
	doSecurity()
	model.CreateRecurringTask("Security", doSecurity, time.Hour*4)
}

func runDiagnosticsJob() {
	doDiagnostics()
	model.CreateRecurringTask("Diagnostics", doDiagnostics, time.Hour*24)
}

func runTokenCleanupJob() {
	doTokenCleanup()
	model.CreateRecurringTask("Token Cleanup", doTokenCleanup, time.Hour*1)
}

func resetStatuses() {
	if result := <-app.Srv.Store.Status().ResetAll(); result.Err != nil {
		l4g.Error(utils.T("mattermost.reset_status.error"), result.Err.Error())
	}
}

func setDiagnosticId() {
	if result := <-app.Srv.Store.System().Get(); result.Err == nil {
		props := result.Data.(model.StringMap)

		id := props[model.SYSTEM_DIAGNOSTIC_ID]
		if len(id) == 0 {
			id = model.NewId()
			systemId := &model.System{Name: model.SYSTEM_DIAGNOSTIC_ID, Value: id}
			<-app.Srv.Store.System().Save(systemId)
		}

		utils.CfgDiagnosticId = id
	}
}

func doSecurity() {
	app.DoSecurityUpdateCheck()
}

func doDiagnostics() {
	if *utils.Cfg.LogSettings.EnableDiagnostics {
		app.SendDailyDiagnostics()
	}
}

func doTokenCleanup() {
	app.Srv.Store.Token().Cleanup()
}
