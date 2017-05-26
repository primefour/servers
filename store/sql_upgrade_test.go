// Copyright (c) 2017-present Mattermost, Inc. All Rights Reserved.
// See License.txt for license information.

package store

import (
	"testing"

	"github.com/primefour/servers/model"
)

func TestStoreUpgrade(t *testing.T) {
	Setup()

	saveSchemaVersion(store.(*SqlStore), VERSION_3_0_0)
	UpgradeDatabase(store.(*SqlStore))

	store.(*SqlStore).SchemaVersion = ""
	UpgradeDatabase(store.(*SqlStore))
}

func TestSaveSchemaVersion(t *testing.T) {
	Setup()

	saveSchemaVersion(store.(*SqlStore), VERSION_3_0_0)
	if result := <-store.System().Get(); result.Err != nil {
		t.Fatal(result.Err)
	} else {
		props := result.Data.(model.StringMap)
		if props["Version"] != VERSION_3_0_0 {
			t.Fatal("version not updated")
		}
	}

	if store.(*SqlStore).SchemaVersion != VERSION_3_0_0 {
		t.Fatal("version not updated")
	}

	saveSchemaVersion(store.(*SqlStore), model.CurrentVersion)
}
