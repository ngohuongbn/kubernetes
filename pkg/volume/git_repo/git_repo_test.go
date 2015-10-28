/*
Copyright 2014 The Kubernetes Authors All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package git_repo

import (
	"io/ioutil"
	"os"
	"path"
	"reflect"
	"strings"
	"testing"

	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/types"
	"k8s.io/kubernetes/pkg/util/exec"
	"k8s.io/kubernetes/pkg/volume"
	"k8s.io/kubernetes/pkg/volume/empty_dir"
)

func newTestHost(t *testing.T) volume.VolumeHost {
	tempDir, err := ioutil.TempDir("/tmp", "git_repo_test.")
	if err != nil {
		t.Fatalf("can't make a temp rootdir: %v", err)
	}
	return volume.NewFakeVolumeHost(tempDir, nil, empty_dir.ProbeVolumePlugins())
}

func TestCanSupport(t *testing.T) {
	plugMgr := volume.VolumePluginMgr{}
	plugMgr.InitPlugins(ProbeVolumePlugins(), newTestHost(t))

	plug, err := plugMgr.FindPluginByName("kubernetes.io/git-repo")
	if err != nil {
		t.Errorf("Can't find the plugin by name")
	}
	if plug.Name() != "kubernetes.io/git-repo" {
		t.Errorf("Wrong name: %s", plug.Name())
	}
	if !plug.CanSupport(&volume.Spec{Volume: &api.Volume{VolumeSource: api.VolumeSource{GitRepo: &api.GitRepoVolumeSource{}}}}) {
		t.Errorf("Expected true")
	}
}

type volSpec struct {
	vol      *api.Volume
	dir      string
	cloneCmd []string
}

func testSetUp(spec volSpec, builder volume.Builder, t *testing.T) {
	var fcmd exec.FakeCmd
	fcmd = exec.FakeCmd{
		CombinedOutputScript: []exec.FakeCombinedOutputAction{
			// git clone
			func() ([]byte, error) {
				os.MkdirAll(path.Join(fcmd.Dirs[0], spec.dir), 0750)
				return []byte{}, nil
			},
			// git checkout
			func() ([]byte, error) { return []byte{}, nil },
			// git reset
			func() ([]byte, error) { return []byte{}, nil },
		},
	}
	fake := exec.FakeExec{
		CommandScript: []exec.FakeCommandAction{
			func(cmd string, args ...string) exec.Cmd { return exec.InitFakeCmd(&fcmd, cmd, args...) },
			func(cmd string, args ...string) exec.Cmd { return exec.InitFakeCmd(&fcmd, cmd, args...) },
			func(cmd string, args ...string) exec.Cmd { return exec.InitFakeCmd(&fcmd, cmd, args...) },
		},
	}
	g := builder.(*gitRepoVolumeBuilder)
	g.exec = &fake

	err := g.SetUp()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	expectedCmds := [][]string{
		spec.cloneCmd,
		{"git", "checkout", g.revision},
		{"git", "reset", "--hard"},
	}
	if fake.CommandCalls != len(expectedCmds) {
		t.Errorf("unexpected command calls: expected 3, saw: %d", fake.CommandCalls)
	}
	if !reflect.DeepEqual(expectedCmds, fcmd.CombinedOutputLog) {
		t.Errorf("unexpected commands: %v, expected: %v", fcmd.CombinedOutputLog, expectedCmds)
	}
	expectedDirs := []string{g.GetPath(), g.GetPath() + spec.dir, g.GetPath() + spec.dir}
	if len(fcmd.Dirs) != 3 || !reflect.DeepEqual(expectedDirs, fcmd.Dirs) {
		t.Errorf("unexpected directories: %v, expected: %v", fcmd.Dirs, expectedDirs)
	}
}

func TestPluginByTarget(t *testing.T) {
	gitUrl := "https://github.com/GoogleCloudPlatform/kubernetes.git"
	specs := []volSpec{
		{
			vol: &api.Volume{
				Name: "vol1",
				VolumeSource: api.VolumeSource{
					GitRepo: &api.GitRepoVolumeSource{
						Repository: gitUrl,
						Revision:   "2a30ce65c5ab586b98916d83385c5983edd353a1",
						Directory:  "target_dir",
					},
				},
			},
			dir:      "/target_dir",
			cloneCmd: []string{"git", "clone", gitUrl, "target_dir"},
		},
		{
			vol: &api.Volume{
				Name: "vol1",
				VolumeSource: api.VolumeSource{
					GitRepo: &api.GitRepoVolumeSource{
						Repository: gitUrl,
						Revision:   "2a30ce65c5ab586b98916d83385c5983edd353a1",
						Directory:  "",
					},
				},
			},
			dir:      "/kubernetes",
			cloneCmd: []string{"git", "clone", gitUrl},
		},
		{
			vol: &api.Volume{
				Name: "vol1",
				VolumeSource: api.VolumeSource{
					GitRepo: &api.GitRepoVolumeSource{
						Repository: gitUrl,
						Revision:   "2a30ce65c5ab586b98916d83385c5983edd353a1",
						Directory:  ".",
					},
				},
			},
			dir:      "",
			cloneCmd: []string{"git", "clone", gitUrl, "."},
		},
	}

	for _, spec := range specs {
		testPluginByVolume(spec, t)
	}
}

func testPluginByVolume(spec volSpec, t *testing.T) {
	plugMgr := volume.VolumePluginMgr{}
	plugMgr.InitPlugins(ProbeVolumePlugins(), newTestHost(t))

	plug, err := plugMgr.FindPluginByName("kubernetes.io/git-repo")
	if err != nil {
		t.Errorf("Can't find the plugin by name")
	}
	pod := &api.Pod{ObjectMeta: api.ObjectMeta{UID: types.UID("poduid")}}
	builder, err := plug.NewBuilder(volume.NewSpecFromVolume(spec.vol), pod, volume.VolumeOptions{RootContext: ""})

	if err != nil {
		t.Errorf("Failed to make a new Builder: %v", err)
	}
	if builder == nil {
		t.Errorf("Got a nil Builder")
	}

	path := builder.GetPath()
	if !strings.HasSuffix(path, "pods/poduid/volumes/kubernetes.io~git-repo/vol1") {
		t.Errorf("Got unexpected path: %s", path)
	}

	testSetUp(spec, builder, t)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			t.Errorf("SetUp() failed, volume path not created: %s", path)
		} else {
			t.Errorf("SetUp() failed: %v", err)
		}
	}

	cleaner, err := plug.NewCleaner("vol1", types.UID("poduid"))
	if err != nil {
		t.Errorf("Failed to make a new Cleaner: %v", err)
	}
	if cleaner == nil {
		t.Errorf("Got a nil Cleaner")
	}

	if err := cleaner.TearDown(); err != nil {
		t.Errorf("Expected success, got: %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Errorf("TearDown() failed, volume path still exists: %s", path)
	} else if !os.IsNotExist(err) {
		t.Errorf("SetUp() failed: %v", err)
	}
}
