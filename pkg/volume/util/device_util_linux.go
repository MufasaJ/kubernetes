// +build linux

/*
Copyright 2016 The Kubernetes Authors.

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

package util

import (
	"errors"
	"path"
	"strings"
	"strconv"
	"github.com/golang/glog"
	"fmt"
)

// FindMultipathDeviceForDevice given a device name like /dev/sdx, find the devicemapper parent
func (handler *deviceHandler) FindMultipathDeviceForDevice(device string) string {
	io := handler.get_io
	disk, err := findDeviceForPath(device, io)
	if err != nil {
		return ""
	}
	sysPath := "/sys/block/"
	if dirs, err := io.ReadDir(sysPath); err == nil {
		for _, f := range dirs {
			name := f.Name()
			if strings.HasPrefix(name, "dm-") {
				if _, err1 := io.Lstat(sysPath + name + "/slaves/" + disk); err1 == nil {
					return "/dev/" + name
				}
			}
		}
	}
	return ""
}

// findDeviceForPath Find the underlaying disk for a linked path such as /dev/disk/by-path/XXXX or /dev/mapper/XXXX
// will return sdX or hdX etc, if /dev/sdX is passed in then sdX will be returned
func findDeviceForPath(path string, io IoUtil) (string, error) {
	devicePath, err := io.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	// if path /dev/hdX split into "", "dev", "hdX" then we will
	// return just the last part
	parts := strings.Split(devicePath, "/")
	if len(parts) == 3 && strings.HasPrefix(parts[1], "dev") {
		return parts[2], nil
	}
	return "", errors.New("Illegal path for device " + devicePath)
}

// FindSlaveDevicesOnMultipath given a dm name like /dev/dm-1, find all devices
// which are managed by the devicemapper dm-1.
func (handler *deviceHandler) FindSlaveDevicesOnMultipath(dm string) []string {
	var devices []string
	io := handler.get_io
	// Split path /dev/dm-1 into "", "dev", "dm-1"
	parts := strings.Split(dm, "/")
	if len(parts) != 3 || !strings.HasPrefix(parts[1], "dev") {
		return devices
	}
	disk := parts[2]
	slavesPath := path.Join("/sys/block/", disk, "/slaves/")
	if files, err := io.ReadDir(slavesPath); err == nil {
		for _, f := range files {
			devices = append(devices, path.Join("/dev/", f.Name()))
		}
	}
	return devices
}

// FindDevicesForLun given an iqn, and lun number, find all the devices
// corresponding to that LUN.
func (handler *deviceHandler) FindDevicesForLun(targetIqn string, lun int) ([]string, error) {
	devices := make([]string, 0)
	io := handler.get_io

	// Iterate over all the iSCSI hosts in sysfs
	sysPath := "/sys/class/iscsi_host"
	hostDirs, err := io.ReadDir(sysPath)
	if err != nil {
		return nil, err
	}
	for _, hostDir := range hostDirs {
		// iSCSI hosts are always of the format "host%d"
		// See drivers/scsi/hosts.c in Linux
		hostName := hostDir.Name()
		if !strings.HasPrefix(hostName, "host") {
			continue
		}
		hostNumber, err := strconv.Atoi(strings.TrimPrefix(hostName, "host"))
		if err != nil {
			glog.Error("Could not get number from iSCSI host: %s", hostName)
			continue
		}

		// Iterate over the children of the iscsi_host device
		// We are looking for the associated session
		devicePath := sysPath + "/" + hostName + "/device"
		deviceDirs, err := io.ReadDir(devicePath)
		if err != nil {
			return nil, err
		}
		for _, deviceDir := range deviceDirs {
			// Skip over files that aren't the session
			// Sessions are of the format "session%u"
			// See drivers/scsi/scsi_transport_iscsi.c in Linux
			sessionName := deviceDir.Name()
			if !strings.HasPrefix(sessionName, "session") {
				continue
			}

			// Read the target name for the iSCSI session
			targetNamePath := devicePath + "/" + sessionName + "/iscsi_session/" + sessionName + "/targetname"
			targetName, err := io.ReadFile(targetNamePath)
			if err != nil {
				return nil, err
			}

			// Only if the session matches the target we were looking for,
			// add it to the map
			if strings.TrimSpace(string(targetName)) != targetIqn {
				continue
			}

			// The list of block devices on the scsi bus will be in a
			// directory called "target%d:%d:%d".
			// See drivers/scsi/scsi_scan.c in Linux
			// We assume the channel/bus and device/controller are always zero for iSCSI
			targetPath := devicePath + "/" + sessionName + fmt.Sprintf("/target%d:0:0", hostNumber)

			// The block device for a given lun will be "%d:%d:%d:%d" --
			// host:channel:bus:LUN
			blockDevicePath := targetPath + fmt.Sprintf("/%d:0:0:%d", hostNumber, lun)

			// If the LUN doesn't exist on this bus, continue on
			_, err = io.Lstat(blockDevicePath)
			if err != nil {
				continue
			}

			// Read the block directory, there should only be one child --
			// the block device "sd*"
			path := blockDevicePath + "/block"
			dirs, err := io.ReadDir(path)
			if err != nil {
				return nil, err
			}
			if 0 < len(dirs) {
				devices = append(devices, dirs[0].Name())
			}
		}
	}

	return devices, nil
}
