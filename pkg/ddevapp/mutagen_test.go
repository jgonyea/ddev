package ddevapp_test

import (
	"fmt"
	"github.com/ddev/ddev/pkg/ddevapp"
	"github.com/ddev/ddev/pkg/dockerutil"
	"github.com/ddev/ddev/pkg/exec"
	"github.com/ddev/ddev/pkg/globalconfig"
	"github.com/ddev/ddev/pkg/util"
	asrt "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestMutagenSimple tests basic mutagen functionality
func TestMutagenSimple(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("TestMutagenSimple takes way too long on Windows, skipping")
	}
	assert := asrt.New(t)

	// Make sure there's not an existing mutagen running, perhaps in wrong directory
	_, _ = exec.RunHostCommand("pkill", "mutagen")

	// Make sure this leaves us in the original test directory
	origDir, _ := os.Getwd()

	// Use Drupal9 as it is a good target for composer failures
	site := FullTestSites[8]
	// We will create directory from scratch, as we'll be removing files and changing it.
	app := &ddevapp.DdevApp{Name: site.Name}
	_ = app.Stop(true, false)
	_ = globalconfig.RemoveProjectInfo(site.Name)

	err := site.Prepare()
	require.NoError(t, err)
	runTime := util.TimeTrack(time.Now(), fmt.Sprintf("%s %s", site.Name, t.Name()))

	err = app.Init(site.Dir)
	assert.NoError(err)
	app.MutagenEnabled = true
	err = app.WriteConfig()
	require.NoError(t, err)

	t.Cleanup(func() {
		runTime()
		err = os.Chdir(origDir)
		assert.NoError(err)
		err = app.Stop(true, false)
		assert.NoError(err)
		assert.False(dockerutil.VolumeExists(ddevapp.GetMutagenVolumeName(app)))
	})
	err = app.Start()
	require.NoError(t, err)

	assert.True(dockerutil.VolumeExists(ddevapp.GetMutagenVolumeName(app)))

	desc, err := app.Describe(false)
	assert.True(desc["mutagen_enabled"].(bool))

	// Make sure the sync is there
	status, short, long, err := app.MutagenStatus()
	assert.NoError(err, "could not run mutagen sync list: status=%s short=%s, long=%s, err=%v", status, short, long, err)
	assert.Equal("ok", status, "wrong status: status=%s short=%s, long=%s", status, short, long)

	// Remove the vendor directory and sync
	err = os.RemoveAll(filepath.Join(app.AppRoot, "vendor"))
	assert.NoError(err)
	err = app.MutagenSyncFlush()
	assert.NoError(err)
	_, stderr, err := app.Exec(&ddevapp.ExecOpts{
		Cmd: "ls -l /var/www/html/vendor",
	})
	assert.Contains(stderr, "cannot access '/var/www/html/vendor'")

	_, _, err = app.Composer([]string{"config", "--no-plugins", "allow-plugins", "true"})
	require.NoError(t, err)

	// Now composer install again and make sure all the stuff comes back
	stdout, _, err := app.Composer([]string{"install", "--no-progress", "--no-interaction"})
	require.NoError(t, err, "stderr=%s, err=%v", stdout, err)
	_, _, err = app.Exec(&ddevapp.ExecOpts{
		Cmd: "ls -l vendor/bin/var-dump-server >/dev/null",
	})
	assert.NoError(err)

	err = app.MutagenSyncFlush()
	assert.NoError(err)
	assert.FileExists(filepath.Join(app.AppRoot, "vendor/bin/var-dump-server"))

	// Stop app, should result in paused sync
	err = app.Stop(false, false)
	status, short, long, err = app.MutagenStatus()
	assert.NoError(err, "could not run mutagen sync list: status=%s short=%s, long=%s, err=%v", status, short, long, err)
	assert.Equal("paused", status, "wrong status: status=%s short=%s, long=%s", status, short, long)

	// Make sure we can stop the daemon
	ddevapp.StopMutagenDaemon()
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		// Verify that the mutagen daemon stopped/died
		sleepWait := time.Second * 1
		if runtime.GOOS == "linux" {
			sleepWait = time.Second * 5
		}
		time.Sleep(sleepWait)
		_, err := exec.RunHostCommand("pkill", "-HUP", "mutagen")
		assert.Error(err)
	}

	mutagenDataDirectory := os.Getenv("MUTAGEN_DATA_DIRECTORY")
	out, err := exec.RunHostCommand(globalconfig.GetMutagenPath(), "sync", "list")
	assert.NoError(err, "mutagen sync list failed with MUTAGEN_DATA_DIRECTORY=%s: out=%s: %v", mutagenDataDirectory, out, err)
	assert.Contains(out, "Started Mutagen daemon in background")
	if !strings.Contains(out, "Started Mutagen daemon in background") && (runtime.GOOS == "darwin" || runtime.GOOS == "linux") {
		out, err := exec.RunHostCommand("bash", "-c", "ps -ef | grep mutagen")
		assert.NoError(err)
		t.Logf("current mutagen processes: \n=====\n%s====\n", out)
	}

	err = app.Start()
	assert.NoError(err)

	// Make sure sync is down on pause also
	err = app.Pause()
	status, short, long, err = app.MutagenStatus()
	assert.NoError(err, "could not run mutagen sync list: status=%s short=%s, long=%s, err=%v", status, short, long, err)
	assert.Equal("paused", status, "wrong status: status=%s short=%s, long=%s", status, short, long)

	// And that it's re-established when we start again
	err = app.Start()
	status, short, long, err = app.MutagenStatus()
	assert.NoError(err, "could not run mutagen sync list: status=%s short=%s, long=%s, err=%v", status, short, long, err)
	assert.Equal("ok", status, "wrong status: status=%s short=%s, long=%s", status, short, long)

	// Make sure mutagen daemon gets stopped on poweoff
	ddevapp.PowerOff()
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		// Verify that the mutagen daemon stopped/died
		sleepWait := time.Second * 1
		if runtime.GOOS == "linux" {
			sleepWait = time.Second * 5
		}
		time.Sleep(sleepWait)
		// pkill -HUP just checks for process existence.
		_, err := exec.RunHostCommand("pkill", "-HUP", "mutagen")
		assert.Error(err)
	}

}
