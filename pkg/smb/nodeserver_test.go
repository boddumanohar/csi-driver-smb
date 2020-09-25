/*
Copyright 2020 The Kubernetes Authors.

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

package smb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"syscall"
	"testing"

	"github.com/kubernetes-csi/csi-driver-smb/pkg/mounter"
	"github.com/kubernetes-csi/csi-driver-smb/test/utils/testutil"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/utils/mount"
)

func TestNodeStageVolume(t *testing.T) {
	stdVolCap := csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{
			Mount: &csi.VolumeCapability_MountVolume{},
		},
	}

	errorMountSensSource := testutil.GetWorkDirPath("error_mount_sens_source", t)
	smbFile := testutil.GetWorkDirPath("smb.go", t)
	sourceTest := testutil.GetWorkDirPath("source_test", t)

	volContext := map[string]string{
		sourceField: "test_source",
	}
	secrets := map[string]string{
		usernameField: "test_username",
		passwordField: "test_password",
		domainField:   "test_doamin",
	}

	tests := []struct {
		desc        string
		req         csi.NodeStageVolumeRequest
		expectedErr testutil.TestError
	}{
		{
			desc: "[Error] Volume ID missing",
			req:  csi.NodeStageVolumeRequest{},
			expectedErr: testutil.TestError{
				DefaultError: status.Error(codes.InvalidArgument, "Volume ID missing in request"),
			},
		},
		{
			desc: "[Error] Volume capabilities missing",
			req:  csi.NodeStageVolumeRequest{VolumeId: "vol_1"},
			expectedErr: testutil.TestError{
				DefaultError: status.Error(codes.InvalidArgument, "Volume capability not provided"),
			},
		},
		{
			desc: "[Error] Stage target path missing",
			req:  csi.NodeStageVolumeRequest{VolumeId: "vol_1", VolumeCapability: &stdVolCap},
			expectedErr: testutil.TestError{
				DefaultError: status.Error(codes.InvalidArgument, "Staging target not provided"),
			},
		},
		{
			desc: "[Error] Source field is missing in context",
			req: csi.NodeStageVolumeRequest{VolumeId: "vol_1", StagingTargetPath: sourceTest,
				VolumeCapability: &stdVolCap},
			expectedErr: testutil.TestError{
				DefaultError: status.Error(codes.InvalidArgument, "source field is missing, current context: map[]"),
			},
		},
		{
			desc: "[Error] Not a Directory",
			req: csi.NodeStageVolumeRequest{VolumeId: "vol_1##", StagingTargetPath: smbFile,
				VolumeCapability: &stdVolCap,
				VolumeContext:    volContext,
				Secrets:          secrets},
			expectedErr: testutil.TestError{
				DefaultError: status.Error(codes.Internal, fmt.Sprintf("MkdirAll %s failed with error: mkdir %s: not a directory", smbFile, smbFile)),
				WindowsError: status.Error(codes.Internal, fmt.Sprintf("Could not mount target %s: mkdir %s: The system cannot find the path specified.", smbFile, smbFile)),
			},
		},
		{
			desc: "[Error] Failed SMB mount mocked by MountSensitive",
			req: csi.NodeStageVolumeRequest{VolumeId: "vol_1##", StagingTargetPath: errorMountSensSource,
				VolumeCapability: &stdVolCap,
				VolumeContext:    volContext,
				Secrets:          secrets},
			expectedErr: testutil.TestError{
				DefaultError: status.Errorf(codes.Internal,
					fmt.Sprintf("volume(vol_1##) mount \"test_source\" on \"%s\" failed with fake MountSensitive: target error",
						errorMountSensSource)),
				// todo: Not a desired error. This will need a better fix
				WindowsError: fmt.Errorf("prepare stage path failed for %s with error: could not cast to csi proxy class", errorMountSensSource),
			},
		},
		{
			desc: "[Success] Valid request",
			req: csi.NodeStageVolumeRequest{VolumeId: "vol_1##", StagingTargetPath: sourceTest,
				VolumeCapability: &stdVolCap,
				VolumeContext:    volContext,
				Secrets:          secrets},
			expectedErr: testutil.TestError{
				// todo: Not a desired error. This will need a better fix
				WindowsError: fmt.Errorf("prepare stage path failed for %s with error: could not cast to csi proxy class", sourceTest),
			},
		},
	}

	// Setup
	d := NewFakeDriver()

	for _, test := range tests {
		fakeMounter := &fakeMounter{}
		d.mounter = &mount.SafeFormatAndMount{
			Interface: fakeMounter,
		}

		_, err := d.NodeStageVolume(context.Background(), &test.req)
		if !testutil.AssertError(&test.expectedErr, err) {
			t.Errorf("test case: %s, \nUnexpected error: %v\n Expected: %v", test.desc, err, test.expectedErr.GetExpectedError())
		}
	}

	// Clean up
	err := os.RemoveAll(sourceTest)
	assert.NoError(t, err)
	err = os.RemoveAll(errorMountSensSource)
	assert.NoError(t, err)
}

func TestNodeGetInfo(t *testing.T) {
	d := NewFakeDriver()

	// Test valid request
	req := csi.NodeGetInfoRequest{}
	resp, err := d.NodeGetInfo(context.Background(), &req)
	assert.NoError(t, err)
	assert.Equal(t, resp.GetNodeId(), fakeNodeID)
}

func TestNodeGetCapabilities(t *testing.T) {
	d := NewFakeDriver()
	capType := &csi.NodeServiceCapability_Rpc{
		Rpc: &csi.NodeServiceCapability_RPC{
			Type: csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
		},
	}
	capList := []*csi.NodeServiceCapability{{
		Type: capType,
	}}
	d.NSCap = capList
	// Test valid request
	req := csi.NodeGetCapabilitiesRequest{}
	resp, err := d.NodeGetCapabilities(context.Background(), &req)
	assert.NotNil(t, resp)
	assert.Equal(t, resp.Capabilities[0].GetType(), capType)
	assert.NoError(t, err)
}

func TestNodeGetVolumeStats(t *testing.T) {
	d := NewFakeDriver()
	req := csi.NodeGetVolumeStatsRequest{}
	resp, err := d.NodeGetVolumeStats(context.Background(), &req)
	assert.Nil(t, resp)
	if !reflect.DeepEqual(err, status.Error(codes.Unimplemented, "")) {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestNodeExpandVolume(t *testing.T) {
	d := NewFakeDriver()
	req := csi.NodeExpandVolumeRequest{}
	resp, err := d.NodeExpandVolume(context.Background(), &req)
	assert.Nil(t, resp)
	if !reflect.DeepEqual(err, status.Error(codes.Unimplemented, "")) {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestNodePublishVolume(t *testing.T) {
	volumeCap := csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER}

	errorMountSource := map[string]string{
		"linux":   "./error_mount_source",
		"windows": "C:\\var\\lib\\kubelet\\error_mount_source",
	}
	alreadyMountedTarget := map[string]string{
		"linux":   "./false_is_likely_exist_target",
		"windows": "C:\\var\\lib\\kubelet\\false_is_likely_exist_target",
	}
	smbFile := map[string]string{
		"linux":   "./smb.go",
		"windows": "C:\\var\\lib\\kubelet\\smb.go",
	}
	sourceTest := map[string]string{
		"linux":   "./source_test",
		"windows": "C:\\var\\lib\\kubelet\\source_test",
	}
	targetTest := map[string]string{
		"linux":   "./target",
		"windows": "C:\\var\\lib\\kubelet\\target_test",
	}

	platform := "linux"
	if runtime.GOOS == "windows" {
		platform = "windows"
	}

	fmt.Println(platform)
	tests := []struct {
		desc               string
		req                csi.NodePublishVolumeRequest
		expectedErrLinux   error
		expectedErrWindows error
	}{
		{
			desc:               "[Error] Volume capabilities missing",
			req:                csi.NodePublishVolumeRequest{},
			expectedErrWindows: status.Error(codes.InvalidArgument, "Volume capability missing in request"),
			expectedErrLinux:   status.Error(codes.InvalidArgument, "Volume capability missing in request"),
		},
		{
			desc:               "[Error] Volume ID missing",
			req:                csi.NodePublishVolumeRequest{VolumeCapability: &csi.VolumeCapability{AccessMode: &volumeCap}},
			expectedErrWindows: status.Error(codes.InvalidArgument, "Volume ID missing in request"),
			expectedErrLinux:   status.Error(codes.InvalidArgument, "Volume ID missing in request"),
		},
		{
			desc: "[Error] Target path missing",
			req: csi.NodePublishVolumeRequest{VolumeCapability: &csi.VolumeCapability{AccessMode: &volumeCap},
				VolumeId: "vol_1"},
			expectedErrWindows: status.Error(codes.InvalidArgument, "Target path not provided"),
			expectedErrLinux:   status.Error(codes.InvalidArgument, "Target path not provided"),
		},
		{
			desc: "[Error] Stage target path missing",
			req: csi.NodePublishVolumeRequest{VolumeCapability: &csi.VolumeCapability{AccessMode: &volumeCap},
				VolumeId:   "vol_1",
				TargetPath: targetTest[platform]},
			expectedErrWindows: status.Error(codes.InvalidArgument, "Staging target not provided"),
			expectedErrLinux:   status.Error(codes.InvalidArgument, "Staging target not provided"),
		},
		{
			desc: "[Error] Not a directory",
			req: csi.NodePublishVolumeRequest{VolumeCapability: &csi.VolumeCapability{AccessMode: &volumeCap},
				VolumeId:          "vol_1",
				TargetPath:        smbFile[platform],
				StagingTargetPath: sourceTest[platform],
				Readonly:          true},
			expectedErrWindows: nil,
			expectedErrLinux:   status.Errorf(codes.Internal, "Could not mount target \"./smb.go\": mkdir ./smb.go: not a directory"),
		},
		{
			desc: "[Error] Mount error mocked by Mount",
			req: csi.NodePublishVolumeRequest{VolumeCapability: &csi.VolumeCapability{AccessMode: &volumeCap},
				VolumeId:          "vol_1",
				TargetPath:        targetTest[platform],
				StagingTargetPath: errorMountSource[platform],
				Readonly:          true},
			expectedErrWindows: nil,
			expectedErrLinux:   status.Errorf(codes.Internal, "Could not mount \"./error_mount_source\" at \"./target_test\": fake Mount: source error"),
		},
		{
			desc: "[Success] Valid request read only",
			req: csi.NodePublishVolumeRequest{VolumeCapability: &csi.VolumeCapability{AccessMode: &volumeCap},
				VolumeId:          "vol_1",
				TargetPath:        targetTest[platform],
				StagingTargetPath: sourceTest[platform],
				Readonly:          true},
			expectedErrWindows: nil,
			expectedErrLinux:   nil,
		},
		{
			desc: "[Success] Valid request already mounted",
			req: csi.NodePublishVolumeRequest{VolumeCapability: &csi.VolumeCapability{AccessMode: &volumeCap},
				VolumeId:          "vol_1",
				TargetPath:        alreadyMountedTarget[platform],
				StagingTargetPath: sourceTest[platform],
				Readonly:          true},
			expectedErrWindows: nil,
			expectedErrLinux:   nil,
		},
		{
			desc: "[Success] Valid request",
			req: csi.NodePublishVolumeRequest{VolumeCapability: &csi.VolumeCapability{AccessMode: &volumeCap},
				VolumeId:          "vol_1",
				TargetPath:        targetTest[platform],
				StagingTargetPath: sourceTest[platform],
				Readonly:          true},
			expectedErrWindows: nil,
			expectedErrLinux:   nil,
		},
	}

	// Setup
	_ = makeDir(alreadyMountedTarget[platform])
	d := NewFakeDriver()

	if runtime.GOOS == "windows" {
		csiProxyMounter, err := mounter.NewSafeMounter()
		assert.NoError(t, err)
		d.mounter = &mount.SafeFormatAndMount{
			Interface: csiProxyMounter.Interface,
		}
	} else {
		fakeMounter := &fakeMounter{}
		d.mounter = &mount.SafeFormatAndMount{
			Interface: fakeMounter,
		}
	}

	for _, test := range tests {
		_, err := d.NodePublishVolume(context.Background(), &test.req)
		expectedErr := test.expectedErrLinux
		if runtime.GOOS == "windows" {
			expectedErr = test.expectedErrWindows
		}
		if !reflect.DeepEqual(err, expectedErr) {
			t.Errorf("test case: %s, Expected Error: %v \n Unexpected error: %v", test.desc, expectedErr, err)
		}
	}

	// Clean up
	err := os.RemoveAll(targetTest[platform])
	assert.NoError(t, err)
	err = os.RemoveAll(alreadyMountedTarget[platform])
	assert.NoError(t, err)
	err = os.RemoveAll(smbFile[platform])
	assert.NoError(t, err)
	err = os.RemoveAll(sourceTest[platform])
}

func TestNodeUnpublishVolume(t *testing.T) {
	skipIfTestingOnWindows(t)
	errorTarget := "./error_is_likely_target"
	targetFile := "./abc.go"
	targetTest := "./target_test"

	tests := []struct {
		desc        string
		req         csi.NodeUnpublishVolumeRequest
		expectedErr error
	}{
		{
			desc:        "[Error] Volume ID missing",
			req:         csi.NodeUnpublishVolumeRequest{TargetPath: targetTest},
			expectedErr: status.Error(codes.InvalidArgument, "Volume ID missing in request"),
		},
		{
			desc:        "[Error] Target missing",
			req:         csi.NodeUnpublishVolumeRequest{VolumeId: "vol_1"},
			expectedErr: status.Error(codes.InvalidArgument, "Target path missing in request"),
		},
		{
			desc:        "[Error] Unmount error mocked by IsLikelyNotMountPoint",
			req:         csi.NodeUnpublishVolumeRequest{TargetPath: errorTarget, VolumeId: "vol_1"},
			expectedErr: status.Error(codes.Internal, "failed to unmount target \"./error_is_likely_target\": fake IsLikelyNotMountPoint: fake error"),
		},
		{
			desc:        "[Success] Valid request",
			req:         csi.NodeUnpublishVolumeRequest{TargetPath: targetFile, VolumeId: "vol_1"},
			expectedErr: nil,
		},
	}

	// Setup
	_ = makeDir(errorTarget)
	d := NewFakeDriver()
	fakeMounter := &fakeMounter{}
	d.mounter = &mount.SafeFormatAndMount{
		Interface: fakeMounter,
	}

	for _, test := range tests {
		_, err := d.NodeUnpublishVolume(context.Background(), &test.req)
		if !reflect.DeepEqual(err, test.expectedErr) {
			fmt.Println(err)
			t.Errorf("test case: %s, Unexpected error: %v", test.desc, err)
		}
	}

	// Clean up
	err := os.RemoveAll(errorTarget)
	assert.NoError(t, err)
}

func TestNodeUnstageVolume(t *testing.T) {
	skipIfTestingOnWindows(t)
	errorTarget := "./error_is_likely_target"
	targetFile := "./abc.go"
	targetTest := "./target_test"

	tests := []struct {
		desc        string
		req         csi.NodeUnstageVolumeRequest
		expectedErr error
	}{
		{
			desc:        "[Error] Volume ID missing",
			req:         csi.NodeUnstageVolumeRequest{StagingTargetPath: targetTest},
			expectedErr: status.Error(codes.InvalidArgument, "Volume ID missing in request"),
		},
		{
			desc:        "[Error] Target missing",
			req:         csi.NodeUnstageVolumeRequest{VolumeId: "vol_1"},
			expectedErr: status.Error(codes.InvalidArgument, "Staging target not provided"),
		},
		{
			desc:        "[Error] CleanupMountPoint error mocked by IsLikelyNotMountPoint",
			req:         csi.NodeUnstageVolumeRequest{StagingTargetPath: errorTarget, VolumeId: "vol_1"},
			expectedErr: status.Error(codes.Internal, "failed to unmount staging target \"./error_is_likely_target\": fake IsLikelyNotMountPoint: fake error"),
		},
		{
			desc:        "[Success] Valid request",
			req:         csi.NodeUnstageVolumeRequest{StagingTargetPath: targetFile, VolumeId: "vol_1"},
			expectedErr: nil,
		},
	}

	// Setup
	_ = makeDir(errorTarget)
	d := NewFakeDriver()
	fakeMounter := &fakeMounter{}
	d.mounter = &mount.SafeFormatAndMount{
		Interface: fakeMounter,
	}

	for _, test := range tests {
		_, err := d.NodeUnstageVolume(context.Background(), &test.req)
		if !reflect.DeepEqual(err, test.expectedErr) {
			t.Errorf("test case: %s, Unexpected error: %v", test.desc, err)
		}
	}

	// Clean up
	err := os.RemoveAll(errorTarget)
	assert.NoError(t, err)
}

func TestEnsureMountPoint(t *testing.T) {
	errorTarget := "./error_is_likely_target"
	alreadyExistTarget := "./false_is_likely_exist_target"
	falseTarget := "./false_is_likely_target"
	smbFile := "./smb.go"
	targetTest := "./target_test"

	tests := []struct {
		desc        string
		target      string
		expectedErr error
	}{
		{
			desc:        "[Error] Mocked by IsLikelyNotMountPoint",
			target:      errorTarget,
			expectedErr: fmt.Errorf("fake IsLikelyNotMountPoint: fake error"),
		},
		{
			desc:        "[Error] Error opening file",
			target:      falseTarget,
			expectedErr: &os.PathError{Op: "open", Path: "./false_is_likely_target", Err: syscall.ENOENT},
		},
		{
			desc:        "[Error] Not a directory",
			target:      smbFile,
			expectedErr: &os.PathError{Op: "mkdir", Path: "./smb.go", Err: syscall.ENOTDIR},
		},
		{
			desc:        "[Success] Successful run",
			target:      targetTest,
			expectedErr: nil,
		},
		{
			desc:        "[Success] Already existing mount",
			target:      alreadyExistTarget,
			expectedErr: nil,
		},
	}

	// Setup
	_ = makeDir(alreadyExistTarget)
	d := NewFakeDriver()
	fakeMounter := &fakeMounter{}
	d.mounter = &mount.SafeFormatAndMount{
		Interface: fakeMounter,
	}

	for _, test := range tests {
		_, err := d.ensureMountPoint(test.target)
		if !reflect.DeepEqual(err, test.expectedErr) {
			t.Errorf("test case: %s, Unexpected error: %v", test.desc, err)
		}
	}

	// Clean up
	err := os.RemoveAll(alreadyExistTarget)
	assert.NoError(t, err)
	err = os.RemoveAll(targetTest)
	assert.NoError(t, err)
}

func TestMakeDir(t *testing.T) {
	targetTest := "./target_test"

	//Successfully create directory
	err := makeDir(targetTest)
	assert.NoError(t, err)

	//Failed case
	err = makeDir("./smb.go")
	var e *os.PathError
	if !errors.As(err, &e) {
		t.Errorf("Unexpected Error: %v", err)
	}

	// Remove the directory created
	err = os.RemoveAll(targetTest)
	assert.NoError(t, err)
}
