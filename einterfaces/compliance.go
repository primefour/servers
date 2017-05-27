// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See License.txt for license information.

package einterfaces

import (
	"github.com/primefour/servers/model"
)

type ComplianceInterface interface {
	StartComplianceDailyJob()
	RunComplianceJob(job *model.Compliance) *model.AppError
}

var theComplianceInterface ComplianceInterface

func RegisterComplianceInterface(newInterface ComplianceInterface) {
	theComplianceInterface = newInterface
}

func GetComplianceInterface() ComplianceInterface {
	return theComplianceInterface
}
