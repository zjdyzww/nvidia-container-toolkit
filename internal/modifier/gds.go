/**
# Copyright (c) 2022, NVIDIA CORPORATION.  All rights reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
**/

package modifier

import (
	"fmt"

	"github.com/NVIDIA/nvidia-container-toolkit/internal/config"
	"github.com/NVIDIA/nvidia-container-toolkit/internal/config/image"
	"github.com/NVIDIA/nvidia-container-toolkit/internal/discover"
	"github.com/NVIDIA/nvidia-container-toolkit/internal/logger"
	"github.com/NVIDIA/nvidia-container-toolkit/internal/oci"
)

const (
	nvidiaGDSEnvvar = "NVIDIA_GDS"
)

// NewGDSModifier creates the modifiers for GDS devices.
// If the spec does not contain the NVIDIA_GDS=enabled environment variable no changes are made.
func NewGDSModifier(logger logger.Interface, cfg *config.Config, ociSpec oci.Spec) (oci.SpecModifier, error) {
	rawSpec, err := ociSpec.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load OCI spec: %v", err)
	}

	image, err := image.NewCUDAImageFromSpec(rawSpec)
	if err != nil {
		return nil, err
	}

	if devices := image.DevicesFromEnvvars(visibleDevicesEnvvar); len(devices.List()) == 0 {
		logger.Infof("No modification required; no devices requested")
		return nil, nil
	}

	if gds, _ := image[nvidiaGDSEnvvar]; gds != "enabled" {
		return nil, nil
	}

	d, err := discover.NewGDSDiscoverer(logger, cfg.NVIDIAContainerCLIConfig.Root)
	if err != nil {
		return nil, fmt.Errorf("failed to construct discoverer for GDS devices: %v", err)
	}

	return NewModifierFromDiscoverer(logger, d)
}
