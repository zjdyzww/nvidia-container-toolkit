/**
# Copyright (c) 2021, NVIDIA CORPORATION.  All rights reserved.
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
*/

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/NVIDIA/nvidia-container-toolkit/internal/system/nvdevices"
	"github.com/NVIDIA/nvidia-container-toolkit/pkg/nvcdi"
	"github.com/NVIDIA/nvidia-container-toolkit/pkg/nvcdi/transform"
	toml "github.com/pelletier/go-toml"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
	"tags.cncf.io/container-device-interface/pkg/cdi"
	"tags.cncf.io/container-device-interface/pkg/parser"
)

const (
	// DefaultNvidiaDriverRoot specifies the default NVIDIA driver run directory
	DefaultNvidiaDriverRoot = "/run/nvidia/driver"

	nvidiaContainerCliSource         = "/usr/bin/nvidia-container-cli"
	nvidiaContainerRuntimeHookSource = "/usr/bin/nvidia-container-runtime-hook"

	nvidiaContainerToolkitConfigSource = "/etc/nvidia-container-runtime/config.toml"
	configFilename                     = "config.toml"
)

type options struct {
	DriverRoot        string
	DriverRootCtrPath string

	ContainerRuntimeMode     string
	ContainerRuntimeDebug    string
	ContainerRuntimeLogLevel string

	ContainerRuntimeModesCdiDefaultKind        string
	ContainerRuntimeModesCDIAnnotationPrefixes cli.StringSlice

	ContainerRuntimeRuntimes cli.StringSlice

	ContainerRuntimeHookSkipModeDetection bool

	ContainerCLIDebug string
	toolkitRoot       string

	cdiEnabled   bool
	cdiOutputDir string
	cdiKind      string
	cdiVendor    string
	cdiClass     string

	acceptNVIDIAVisibleDevicesWhenUnprivileged bool
	acceptNVIDIAVisibleDevicesAsVolumeMounts   bool

	ignoreErrors bool
}

func main() {

	opts := options{}

	// Create the top-level CLI
	c := cli.NewApp()
	c.Name = "toolkit"
	c.Usage = "Manage the NVIDIA container toolkit"
	c.Version = "0.1.0"

	// Create the 'install' subcommand
	install := cli.Command{}
	install.Name = "install"
	install.Usage = "Install the components of the NVIDIA container toolkit"
	install.ArgsUsage = "<toolkit_directory>"
	install.Before = func(c *cli.Context) error {
		return validateOptions(c, &opts)
	}
	install.Action = func(c *cli.Context) error {
		return Install(c, &opts)
	}

	// Create the 'delete' command
	delete := cli.Command{}
	delete.Name = "delete"
	delete.Usage = "Delete the NVIDIA container toolkit"
	delete.ArgsUsage = "<toolkit_directory>"
	delete.Before = func(c *cli.Context) error {
		return validateOptions(c, &opts)
	}
	delete.Action = func(c *cli.Context) error {
		return Delete(c, &opts)
	}

	// Register the subcommand with the top-level CLI
	c.Commands = []*cli.Command{
		&install,
		&delete,
	}

	flags := []cli.Flag{
		&cli.StringFlag{
			Name:        "nvidia-driver-root",
			Value:       DefaultNvidiaDriverRoot,
			Destination: &opts.DriverRoot,
			EnvVars:     []string{"NVIDIA_DRIVER_ROOT"},
		},
		&cli.StringFlag{
			Name:        "driver-root-ctr-path",
			Value:       DefaultNvidiaDriverRoot,
			Destination: &opts.DriverRootCtrPath,
			EnvVars:     []string{"DRIVER_ROOT_CTR_PATH"},
		},
		&cli.StringFlag{
			Name:        "nvidia-container-runtime.debug",
			Aliases:     []string{"nvidia-container-runtime-debug"},
			Usage:       "Specify the location of the debug log file for the NVIDIA Container Runtime",
			Destination: &opts.ContainerRuntimeDebug,
			EnvVars:     []string{"NVIDIA_CONTAINER_RUNTIME_DEBUG"},
		},
		&cli.StringFlag{
			Name:        "nvidia-container-runtime.log-level",
			Aliases:     []string{"nvidia-container-runtime-debug-log-level"},
			Destination: &opts.ContainerRuntimeLogLevel,
			EnvVars:     []string{"NVIDIA_CONTAINER_RUNTIME_LOG_LEVEL"},
		},
		&cli.StringFlag{
			Name:        "nvidia-container-runtime.mode",
			Aliases:     []string{"nvidia-container-runtime-mode"},
			Destination: &opts.ContainerRuntimeMode,
			EnvVars:     []string{"NVIDIA_CONTAINER_RUNTIME_MODE"},
		},
		&cli.StringFlag{
			Name:        "nvidia-container-runtime.modes.cdi.default-kind",
			Destination: &opts.ContainerRuntimeModesCdiDefaultKind,
			EnvVars:     []string{"NVIDIA_CONTAINER_RUNTIME_MODES_CDI_DEFAULT_KIND"},
		},
		&cli.StringSliceFlag{
			Name:        "nvidia-container-runtime.modes.cdi.annotation-prefixes",
			Destination: &opts.ContainerRuntimeModesCDIAnnotationPrefixes,
			EnvVars:     []string{"NVIDIA_CONTAINER_RUNTIME_MODES_CDI_ANNOTATION_PREFIXES"},
		},
		&cli.StringSliceFlag{
			Name:        "nvidia-container-runtime.runtimes",
			Destination: &opts.ContainerRuntimeRuntimes,
			EnvVars:     []string{"NVIDIA_CONTAINER_RUNTIME_RUNTIMES"},
		},
		&cli.BoolFlag{
			Name:        "nvidia-container-runtime-hook.skip-mode-detection",
			Value:       true,
			Destination: &opts.ContainerRuntimeHookSkipModeDetection,
			EnvVars:     []string{"NVIDIA_CONTAINER_RUNTIME_HOOK_SKIP_MODE_DETECTION"},
		},
		&cli.StringFlag{
			Name:        "nvidia-container-cli.debug",
			Aliases:     []string{"nvidia-container-cli-debug"},
			Usage:       "Specify the location of the debug log file for the NVIDIA Container CLI",
			Destination: &opts.ContainerCLIDebug,
			EnvVars:     []string{"NVIDIA_CONTAINER_CLI_DEBUG"},
		},
		&cli.BoolFlag{
			Name:        "accept-nvidia-visible-devices-envvar-when-unprivileged",
			Usage:       "Set the accept-nvidia-visible-devices-envvar-when-unprivileged config option",
			Value:       true,
			Destination: &opts.acceptNVIDIAVisibleDevicesWhenUnprivileged,
			EnvVars:     []string{"ACCEPT_NVIDIA_VISIBLE_DEVICES_ENVVAR_WHEN_UNPRIVILEGED"},
		},
		&cli.BoolFlag{
			Name:        "accept-nvidia-visible-devices-as-volume-mounts",
			Usage:       "Set the accept-nvidia-visible-devices-as-volume-mounts config option",
			Destination: &opts.acceptNVIDIAVisibleDevicesAsVolumeMounts,
			EnvVars:     []string{"ACCEPT_NVIDIA_VISIBLE_DEVICES_AS_VOLUME_MOUNTS"},
		},
		&cli.StringFlag{
			Name:        "toolkit-root",
			Usage:       "The directory where the NVIDIA Container toolkit is to be installed",
			Required:    true,
			Destination: &opts.toolkitRoot,
			EnvVars:     []string{"TOOLKIT_ROOT"},
		},
		&cli.BoolFlag{
			Name:        "cdi-enabled",
			Aliases:     []string{"enable-cdi"},
			Usage:       "enable the generation of a CDI specification",
			Destination: &opts.cdiEnabled,
			EnvVars:     []string{"CDI_ENABLED", "ENABLE_CDI"},
		},
		&cli.StringFlag{
			Name:        "cdi-output-dir",
			Usage:       "the directory where the CDI output files are to be written. If this is set to '', no CDI specification is generated.",
			Value:       "/var/run/cdi",
			Destination: &opts.cdiOutputDir,
			EnvVars:     []string{"CDI_OUTPUT_DIR"},
		},
		&cli.StringFlag{
			Name:        "cdi-kind",
			Usage:       "the vendor string to use for the generated CDI specification",
			Value:       "management.nvidia.com/gpu",
			Destination: &opts.cdiKind,
			EnvVars:     []string{"CDI_KIND"},
		},
		&cli.BoolFlag{
			Name:        "ignore-errors",
			Usage:       "ignore errors when installing the NVIDIA Container toolkit. This is used for testing purposes only.",
			Hidden:      true,
			Destination: &opts.ignoreErrors,
		},
	}

	// Update the subcommand flags with the common subcommand flags
	install.Flags = append([]cli.Flag{}, flags...)
	delete.Flags = append([]cli.Flag{}, flags...)

	// Run the top-level CLI
	if err := c.Run(os.Args); err != nil {
		log.Fatal(fmt.Errorf("error: %v", err))
	}
}

// validateOptions checks whether the specified options are valid
func validateOptions(c *cli.Context, opts *options) error {
	if opts.toolkitRoot == "" {
		return fmt.Errorf("invalid --toolkit-root option: %v", opts.toolkitRoot)
	}

	vendor, class := parser.ParseQualifier(opts.cdiKind)
	if err := parser.ValidateVendorName(vendor); err != nil {
		return fmt.Errorf("invalid CDI vendor name: %v", err)
	}
	if err := parser.ValidateClassName(class); err != nil {
		return fmt.Errorf("invalid CDI class name: %v", err)
	}
	opts.cdiVendor = vendor
	opts.cdiClass = class

	return nil
}

// Delete removes the NVIDIA container toolkit
func Delete(cli *cli.Context, opts *options) error {
	log.Infof("Deleting NVIDIA container toolkit from '%v'", opts.toolkitRoot)
	err := os.RemoveAll(opts.toolkitRoot)
	if err != nil {
		return fmt.Errorf("error deleting toolkit directory: %v", err)
	}
	return nil
}

// Install installs the components of the NVIDIA container toolkit.
// Any existing installation is removed.
func Install(cli *cli.Context, opts *options) error {
	log.Infof("Installing NVIDIA container toolkit to '%v'", opts.toolkitRoot)

	log.Infof("Removing existing NVIDIA container toolkit installation")
	err := os.RemoveAll(opts.toolkitRoot)
	if err != nil && !opts.ignoreErrors {
		return fmt.Errorf("error removing toolkit directory: %v", err)
	} else if err != nil {
		log.Errorf("Ignoring error: %v", fmt.Errorf("error removing toolkit directory: %v", err))
	}

	toolkitConfigDir := filepath.Join(opts.toolkitRoot, ".config", "nvidia-container-runtime")
	toolkitConfigPath := filepath.Join(toolkitConfigDir, configFilename)

	err = createDirectories(opts.toolkitRoot, toolkitConfigDir)
	if err != nil && !opts.ignoreErrors {
		return fmt.Errorf("could not create required directories: %v", err)
	} else if err != nil {
		log.Errorf("Ignoring error: %v", fmt.Errorf("could not create required directories: %v", err))
	}

	err = installContainerLibraries(opts.toolkitRoot)
	if err != nil && !opts.ignoreErrors {
		return fmt.Errorf("error installing NVIDIA container library: %v", err)
	} else if err != nil {
		log.Errorf("Ignoring error: %v", fmt.Errorf("error installing NVIDIA container library: %v", err))
	}

	err = installContainerRuntimes(opts.toolkitRoot, opts.DriverRoot)
	if err != nil && !opts.ignoreErrors {
		return fmt.Errorf("error installing NVIDIA container runtime: %v", err)
	} else if err != nil {
		log.Errorf("Ignoring error: %v", fmt.Errorf("error installing NVIDIA container runtime: %v", err))
	}

	nvidiaContainerCliExecutable, err := installContainerCLI(opts.toolkitRoot)
	if err != nil && !opts.ignoreErrors {
		return fmt.Errorf("error installing NVIDIA container CLI: %v", err)
	} else if err != nil {
		log.Errorf("Ignoring error: %v", fmt.Errorf("error installing NVIDIA container CLI: %v", err))
	}

	nvidiaContainerRuntimeHookPath, err := installRuntimeHook(opts.toolkitRoot, toolkitConfigPath)
	if err != nil && !opts.ignoreErrors {
		return fmt.Errorf("error installing NVIDIA container runtime hook: %v", err)
	} else if err != nil {
		log.Errorf("Ignoring error: %v", fmt.Errorf("error installing NVIDIA container runtime hook: %v", err))
	}

	nvidiaCTKPath, err := installContainerToolkitCLI(opts.toolkitRoot)
	if err != nil && !opts.ignoreErrors {
		return fmt.Errorf("error installing NVIDIA Container Toolkit CLI: %v", err)
	} else if err != nil {
		log.Errorf("Ignoring error: %v", fmt.Errorf("error installing NVIDIA Container Toolkit CLI: %v", err))
	}

	err = installToolkitConfig(cli, toolkitConfigPath, nvidiaContainerCliExecutable, nvidiaCTKPath, nvidiaContainerRuntimeHookPath, opts)
	if err != nil && !opts.ignoreErrors {
		return fmt.Errorf("error installing NVIDIA container toolkit config: %v", err)
	} else if err != nil {
		log.Errorf("Ignoring error: %v", fmt.Errorf("error installing NVIDIA container toolkit config: %v", err))
	}

	return generateCDISpec(opts, nvidiaCTKPath)
}

// installContainerLibraries locates and installs the libraries that are part of
// the nvidia-container-toolkit.
// A predefined set of library candidates are considered, with the first one
// resulting in success being installed to the toolkit folder. The install process
// resolves the symlink for the library and copies the versioned library itself.
func installContainerLibraries(toolkitRoot string) error {
	log.Infof("Installing NVIDIA container library to '%v'", toolkitRoot)

	libs := []string{
		"libnvidia-container.so.1",
		"libnvidia-container-go.so.1",
	}

	for _, l := range libs {
		err := installLibrary(l, toolkitRoot)
		if err != nil {
			return fmt.Errorf("failed to install %s: %v", l, err)
		}
	}

	return nil
}

// installLibrary installs the specified library to the toolkit directory.
func installLibrary(libName string, toolkitRoot string) error {
	libraryPath, err := findLibrary("", libName)
	if err != nil {
		return fmt.Errorf("error locating NVIDIA container library: %v", err)
	}

	installedLibPath, err := installFileToFolder(toolkitRoot, libraryPath)
	if err != nil {
		return fmt.Errorf("error installing %v to %v: %v", libraryPath, toolkitRoot, err)
	}
	log.Infof("Installed '%v' to '%v'", libraryPath, installedLibPath)

	if filepath.Base(installedLibPath) == libName {
		return nil
	}

	err = installSymlink(toolkitRoot, libName, installedLibPath)
	if err != nil {
		return fmt.Errorf("error installing symlink for NVIDIA container library: %v", err)
	}

	return nil
}

// installToolkitConfig installs the config file for the NVIDIA container toolkit ensuring
// that the settings are updated to match the desired install and nvidia driver directories.
func installToolkitConfig(c *cli.Context, toolkitConfigPath string, nvidiaContainerCliExecutablePath string, nvidiaCTKPath string, nvidaContainerRuntimeHookPath string, opts *options) error {
	log.Infof("Installing NVIDIA container toolkit config '%v'", toolkitConfigPath)

	config, err := loadConfig(nvidiaContainerToolkitConfigSource)
	if err != nil {
		return fmt.Errorf("could not open source config file: %v", err)
	}

	targetConfig, err := os.Create(toolkitConfigPath)
	if err != nil {
		return fmt.Errorf("could not create target config file: %v", err)
	}
	defer targetConfig.Close()

	// Read the ldconfig path from the config as this may differ per platform
	// On ubuntu-based systems this ends in `.real`
	ldconfigPath := fmt.Sprintf("%s", config.GetDefault("nvidia-container-cli.ldconfig", "/sbin/ldconfig"))
	// Use the driver run root as the root:
	driverLdconfigPath := "@" + filepath.Join(opts.DriverRoot, strings.TrimPrefix(ldconfigPath, "@/"))

	configValues := map[string]interface{}{
		// Set the options in the root toml table
		"accept-nvidia-visible-devices-envvar-when-unprivileged": opts.acceptNVIDIAVisibleDevicesWhenUnprivileged,
		"accept-nvidia-visible-devices-as-volume-mounts":         opts.acceptNVIDIAVisibleDevicesAsVolumeMounts,
		// Set the nvidia-container-cli options
		"nvidia-container-cli.root":     opts.DriverRoot,
		"nvidia-container-cli.path":     nvidiaContainerCliExecutablePath,
		"nvidia-container-cli.ldconfig": driverLdconfigPath,
		// Set nvidia-ctk options
		"nvidia-ctk.path": nvidiaCTKPath,
		// Set the nvidia-container-runtime-hook options
		"nvidia-container-runtime-hook.path":                nvidaContainerRuntimeHookPath,
		"nvidia-container-runtime-hook.skip-mode-detection": opts.ContainerRuntimeHookSkipModeDetection,
	}
	for key, value := range configValues {
		config.Set(key, value)
	}

	// Set the optional config options
	optionalConfigValues := map[string]interface{}{
		"nvidia-container-runtime.debug":                         opts.ContainerRuntimeDebug,
		"nvidia-container-runtime.log-level":                     opts.ContainerRuntimeLogLevel,
		"nvidia-container-runtime.mode":                          opts.ContainerRuntimeMode,
		"nvidia-container-runtime.modes.cdi.annotation-prefixes": opts.ContainerRuntimeModesCDIAnnotationPrefixes,
		"nvidia-container-runtime.modes.cdi.default-kind":        opts.ContainerRuntimeModesCdiDefaultKind,
		"nvidia-container-runtime.runtimes":                      opts.ContainerRuntimeRuntimes,
		"nvidia-container-cli.debug":                             opts.ContainerCLIDebug,
	}
	for key, value := range optionalConfigValues {
		if !c.IsSet(key) {
			log.Infof("Skipping unset option: %v", key)
			continue
		}
		if value == nil {
			log.Infof("Skipping option with nil value: %v", key)
			continue
		}

		switch v := value.(type) {
		case string:
			if v == "" {
				continue
			}
		case cli.StringSlice:
			if len(v.Value()) == 0 {
				continue
			}
			value = v.Value()
		default:
			log.Warningf("Unexpected type for option %v=%v: %T", key, value, v)
		}

		config.Set(key, value)
	}

	if _, err := config.WriteTo(targetConfig); err != nil {
		return fmt.Errorf("error writing config: %v", err)
	}

	os.Stdout.WriteString("Using config:\n")
	if _, err = config.WriteTo(os.Stdout); err != nil {
		log.Warningf("Failed to output config to STDOUT: %v", err)
	}

	return nil
}

func loadConfig(path string) (*toml.Tree, error) {
	_, err := os.Stat(path)
	if err == nil {
		return toml.LoadFile(path)
	} else if os.IsNotExist(err) {
		return toml.TreeFromMap(nil)
	}
	return nil, err
}

// installContainerToolkitCLI installs the nvidia-ctk CLI executable and wrapper.
func installContainerToolkitCLI(toolkitDir string) (string, error) {
	e := executable{
		source: "/usr/bin/nvidia-ctk",
		target: executableTarget{
			dotfileName: "nvidia-ctk.real",
			wrapperName: "nvidia-ctk",
		},
	}

	return e.install(toolkitDir)
}

// installContainerCLI sets up the NVIDIA container CLI executable, copying the executable
// and implementing the required wrapper
func installContainerCLI(toolkitRoot string) (string, error) {
	log.Infof("Installing NVIDIA container CLI from '%v'", nvidiaContainerCliSource)

	env := map[string]string{
		"LD_LIBRARY_PATH": toolkitRoot,
	}

	e := executable{
		source: nvidiaContainerCliSource,
		target: executableTarget{
			dotfileName: "nvidia-container-cli.real",
			wrapperName: "nvidia-container-cli",
		},
		env: env,
	}

	installedPath, err := e.install(toolkitRoot)
	if err != nil {
		return "", fmt.Errorf("error installing NVIDIA container CLI: %v", err)
	}
	return installedPath, nil
}

// installRuntimeHook sets up the NVIDIA runtime hook, copying the executable
// and implementing the required wrapper
func installRuntimeHook(toolkitRoot string, configFilePath string) (string, error) {
	log.Infof("Installing NVIDIA container runtime hook from '%v'", nvidiaContainerRuntimeHookSource)

	argLines := []string{
		fmt.Sprintf("-config \"%s\"", configFilePath),
	}

	e := executable{
		source: nvidiaContainerRuntimeHookSource,
		target: executableTarget{
			dotfileName: "nvidia-container-runtime-hook.real",
			wrapperName: "nvidia-container-runtime-hook",
		},
		argLines: argLines,
	}

	installedPath, err := e.install(toolkitRoot)
	if err != nil {
		return "", fmt.Errorf("error installing NVIDIA container runtime hook: %v", err)
	}

	err = installSymlink(toolkitRoot, "nvidia-container-toolkit", installedPath)
	if err != nil {
		return "", fmt.Errorf("error installing symlink to NVIDIA container runtime hook: %v", err)
	}

	return installedPath, nil
}

// installSymlink creates a symlink in the toolkitDirectory that points to the specified target.
// Note: The target is assumed to be local to the toolkit directory
func installSymlink(toolkitRoot string, link string, target string) error {
	symlinkPath := filepath.Join(toolkitRoot, link)
	targetPath := filepath.Base(target)
	log.Infof("Creating symlink '%v' -> '%v'", symlinkPath, targetPath)

	err := os.Symlink(targetPath, symlinkPath)
	if err != nil {
		return fmt.Errorf("error creating symlink '%v' => '%v': %v", symlinkPath, targetPath, err)
	}
	return nil
}

// installFileToFolder copies a source file to a destination folder.
// The path of the input file is ignored.
// e.g. installFileToFolder("/some/path/file.txt", "/output/path")
// will result in a file "/output/path/file.txt" being generated
func installFileToFolder(destFolder string, src string) (string, error) {
	name := filepath.Base(src)
	return installFileToFolderWithName(destFolder, name, src)
}

// cp src destFolder/name
func installFileToFolderWithName(destFolder string, name, src string) (string, error) {
	dest := filepath.Join(destFolder, name)
	err := installFile(dest, src)
	if err != nil {
		return "", fmt.Errorf("error copying '%v' to '%v': %v", src, dest, err)
	}
	return dest, nil
}

// installFile copies a file from src to dest and maintains
// file modes
func installFile(dest string, src string) error {
	log.Infof("Installing '%v' to '%v'", src, dest)

	source, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("error opening source: %v", err)
	}
	defer source.Close()

	destination, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("error creating destination: %v", err)
	}
	defer destination.Close()

	_, err = io.Copy(destination, source)
	if err != nil {
		return fmt.Errorf("error copying file: %v", err)
	}

	err = applyModeFromSource(dest, src)
	if err != nil {
		return fmt.Errorf("error setting destination file mode: %v", err)
	}
	return nil
}

// applyModeFromSource sets the file mode for a destination file
// to match that of a specified source file
func applyModeFromSource(dest string, src string) error {
	sourceInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("error getting file info for '%v': %v", src, err)
	}
	err = os.Chmod(dest, sourceInfo.Mode())
	if err != nil {
		return fmt.Errorf("error setting mode for '%v': %v", dest, err)
	}
	return nil
}

// findLibrary searches a set of candidate libraries in the specified root for
// a given library name
func findLibrary(root string, libName string) (string, error) {
	log.Infof("Finding library %v (root=%v)", libName, root)

	candidateDirs := []string{
		"/usr/lib64",
		"/usr/lib/x86_64-linux-gnu",
		"/usr/lib/aarch64-linux-gnu",
	}

	for _, d := range candidateDirs {
		l := filepath.Join(root, d, libName)
		log.Infof("Checking library candidate '%v'", l)

		libraryCandidate, err := resolveLink(l)
		if err != nil {
			log.Infof("Skipping library candidate '%v': %v", l, err)
			continue
		}

		return libraryCandidate, nil
	}

	return "", fmt.Errorf("error locating library '%v'", libName)
}

// resolveLink finds the target of a symlink or the file itself in the
// case of a regular file.
// This is equivalent to running `readlink -f ${l}`
func resolveLink(l string) (string, error) {
	resolved, err := filepath.EvalSymlinks(l)
	if err != nil {
		return "", fmt.Errorf("error resolving link '%v': %v", l, err)
	}
	if l != resolved {
		log.Infof("Resolved link: '%v' => '%v'", l, resolved)
	}
	return resolved, nil
}

func createDirectories(dir ...string) error {
	for _, d := range dir {
		log.Infof("Creating directory '%v'", d)
		err := os.MkdirAll(d, 0755)
		if err != nil {
			return fmt.Errorf("error creating directory: %v", err)
		}
	}
	return nil
}

// generateCDISpec generates a CDI spec for use in managemnt containers
func generateCDISpec(opts *options, nvidiaCTKPath string) error {
	if !opts.cdiEnabled {
		return nil
	}
	if opts.cdiOutputDir == "" {
		log.Info("Skipping CDI spec generation (no output directory specified)")
		return nil
	}

	log.Infof("Creating control device nodes at %v", opts.DriverRootCtrPath)
	devices, err := nvdevices.New(
		nvdevices.WithDevRoot(opts.DriverRootCtrPath),
	)
	if err != nil {
		return fmt.Errorf("failed to create library: %v", err)
	}
	if err := devices.CreateNVIDIAControlDevices(); err != nil {
		return fmt.Errorf("failed to create control device nodes: %v", err)
	}

	log.Info("Generating CDI spec for management containers")
	cdilib, err := nvcdi.New(
		nvcdi.WithMode(nvcdi.ModeManagement),
		nvcdi.WithDriverRoot(opts.DriverRootCtrPath),
		nvcdi.WithNVIDIACTKPath(nvidiaCTKPath),
		nvcdi.WithVendor(opts.cdiVendor),
		nvcdi.WithClass(opts.cdiClass),
	)
	if err != nil {
		return fmt.Errorf("failed to create CDI library for management containers: %v", err)
	}

	spec, err := cdilib.GetSpec()
	if err != nil {
		return fmt.Errorf("failed to genereate CDI spec for management containers: %v", err)
	}
	err = transform.NewRootTransformer(
		opts.DriverRootCtrPath,
		opts.DriverRoot,
	).Transform(spec.Raw())
	if err != nil {
		return fmt.Errorf("failed to transform driver root in CDI spec: %v", err)
	}

	name, err := cdi.GenerateNameForSpec(spec.Raw())
	if err != nil {
		return fmt.Errorf("failed to generate CDI name for management containers: %v", err)
	}
	err = spec.Save(filepath.Join(opts.cdiOutputDir, name))
	if err != nil {
		return fmt.Errorf("failed to save CDI spec for management containers: %v", err)
	}

	return nil
}
