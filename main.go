package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	kubeconfigDir  = ".kube"
	kubeconfigFile = "config"
	version        = "0.5.0"
)

type Container struct {
	Name   string
	CPU    int
	Memory string
	GPU    int
}

type Partition struct {
	Name         string
	DisplayName  string // User-friendly display name for the partition
	Description  string
	GPUTag       string
	GPUName      string
	Images       []string
	CPULimit     int
	MemoryLimit  int    // in GiB
	StorageClass string // Storage class for this partition (e.g., "yanyuan-nfs", "wm2-nfs")
}

// GetDisplayName returns the display name if set, otherwise falls back to Name
func (p Partition) GetDisplayName() string {
	if p.DisplayName != "" {
		return p.DisplayName
	}
	return p.Name
}

// findPartition finds a partition by Name or DisplayName
// Returns the partition and true if found, empty partition and false otherwise
func findPartition(partitions []Partition, input string) (Partition, bool) {
	// First try exact match on Name
	for _, p := range partitions {
		if p.Name == input {
			return p, true
		}
	}
	// Then try exact match on DisplayName
	for _, p := range partitions {
		if p.DisplayName == input {
			return p, true
		}
	}
	// Finally try case-insensitive match on DisplayName
	inputLower := strings.ToLower(input)
	for _, p := range partitions {
		if strings.ToLower(p.DisplayName) == inputLower {
			return p, true
		}
	}
	return Partition{}, false
}

type PersistentVolume struct {
	Name         string
	Size         string
	StorageClass string
	AccessMode   string
	Status       string
	IsDefault    bool
}

func main() {
	if len(os.Args) < 2 {
		printHelp()
		return
	}

	command := os.Args[1]

	switch command {
	case "install":
		install()
	case "help":
		printHelp()
	case "version":
		fmt.Printf("HPCGame CLI version %s\n", version)
	// Original commands
	case "create":
		createContainer()
	case "shell":
		shellContainer()
	case "ls":
		listContainers()
	case "lspart":
		listPartitions(getPartitions())
	case "delete":
		deleteContainer()
	// Docker-like commands
	case "run":
		runContainer()
	case "ps", "container", "containers":
		listContainers()
	case "images", "image":
		listImages()
	case "exec":
		execInContainer()
	case "cp":
		copyFiles()
	case "port", "ports", "portforward":
		portForward()
	case "pull":
		fmt.Println("Images are pre-pulled in the HPCGame environment")
	case "rm", "kill", "stop":
		deleteContainer()
	case "volume", "volumes":
		handleVolumeCommands()
	default:
		fmt.Printf("Unknown command: %s\n", command)
		printHelp()
	}
}

func printHelp() {
	helpText := `HPCGame CLI Tool with Docker-compatible commands

Usage:
  hpcgame <command> [options]

Original Commands:
  install         Install and configure required components
  create          Create a new container
  ls              List containers for current account
  lspart          List available partitions
  shell           Connect to container terminal
  delete          Delete a container
  portforward     Set up port forwarding
  volume          Manage persistent volumes

Docker-compatible Commands:
  run             Create and run a new container (alternative to create)
  ps              List running containers (same as ls)
  images          List available images for each partition
  exec            Execute a command in a running container
  cp              Copy files between local and container
  rm              Remove a container (same as delete)
  port            Forward port (same as portforward)

Options for create/run command:
  -p, --partition STRING  Specify partition name
  -c, --cpu INT           Number of CPUs
  -m, --memory INT        Memory in GiB
  -g, --gpu INT           Number of GPUs (default: 0)
  -d, --duration INT      Pod maximum runtime in seconds (default: 7200, max: 86400 = 1 day)
  -v, --volume LIST       Mount volumes (comma-separated)
  -i, --image STRING      Specify container image
  -n, --name STRING       Assign a name to the container
  
Examples:
  # Create a container with 4 CPUs and 8GiB RAM in the x86 partition
  hpcgame create -p x86 -c 4 -m 8
  
  # Docker-style alternative to create container
  hpcgame run -p gpu -g 1 -v my-data,shared-data -n my-gpu-container pytorch/pytorch
  
  # Connect to container shell (original method)
  hpcgame shell my-container
  
  # Execute commands in container (Docker-style)
  hpcgame exec -it my-container bash
  
  # Copy files to/from a container
  hpcgame cp ./local-file.txt my-container:/path/file.txt
  hpcgame cp my-container:/path/file.txt ./local-copy.txt
  
  # Port forwarding
  hpcgame portforward my-container 8080:80
  # or Docker-style alternative
  hpcgame port my-container 8080:80

Volume Commands:
  hpcgame volume ls                                     List all volumes
  hpcgame volume create NAME SIZE STORAGE_CLASS [MODE]  Create a new volume
  hpcgame volume rm NAME                                Delete a volume

Note:
  - Default partition volume is automatically mounted to /partition-data
  - Additional volumes are mounted to /mnt/VOLUME_NAME
`
	fmt.Println(helpText)
}

func install() {
	// 1. Check if kubectl is installed
	if !checkKubectlInstalled() {
		installKubectl()
	} else {
		fmt.Println("✅ kubectl is already installed")
	}

	// 2. Get kubeconfig from user and validate
	kubeconfig := getKubeconfigFromUser()
	if !validateKubeconfig(kubeconfig) {
		fmt.Println("❌ Invalid kubeconfig provided. Please check and try again.")
		return
	}

	// 3. Save kubeconfig
	saveKubeconfig(kubeconfig)

	// 4. Install VSCode extensions if available
	installVSCodeExtensions()

	// 5. Get partition information
	partitions := getPartitions()
	if partitions == nil {
		fmt.Println("❌ Failed to get partition information. Please check your network connection.")
		return
	}

	// 6. Display partition information
	listPartitions(partitions)

	fmt.Println("✅ Installation complete")
}

func checkKubectlInstalled() bool {
	_, err := getKubectlPath()
	return err == nil
}

// getKubectlPath returns the absolute path to kubectl executable
func getKubectlPath() (string, error) {
	// First, check if kubectl exists in ~/.hpcgame/ (our install location)
	homeDir, err := os.UserHomeDir()
	if err == nil {
		var kubectlName string
		if runtime.GOOS == "windows" {
			kubectlName = "kubectl.exe"
		} else {
			kubectlName = "kubectl"
		}
		hpcgamePath := filepath.Join(homeDir, ".hpcgame", kubectlName)
		if _, err := os.Stat(hpcgamePath); err == nil {
			return hpcgamePath, nil
		}
	}

	// Fallback to PATH lookup
	path, err := exec.LookPath("kubectl")
	// On Windows, Go 1.19+ returns exec.ErrDot when executable is in current directory
	// We need to handle this by converting to absolute path
	if errors.Is(err, exec.ErrDot) {
		absPath, absErr := filepath.Abs(path)
		if absErr != nil {
			return "", err // return original error if Abs fails
		}
		return absPath, nil
	}
	if err != nil {
		return "", err
	}
	// Also convert relative paths to absolute on Windows (for other cases)
	if runtime.GOOS == "windows" && !filepath.IsAbs(path) {
		absPath, absErr := filepath.Abs(path)
		if absErr != nil {
			return "", absErr
		}
		return absPath, nil
	}
	return path, nil
}

// kubectlCommand creates an exec.Cmd for kubectl with proper path resolution
func kubectlCommand(args ...string) *exec.Cmd {
	kubectlPath, err := getKubectlPath()
	if err != nil {
		// Fallback to "kubectl" - will fail later with clear error
		kubectlPath = "kubectl"
	}
	return exec.Command(kubectlPath, args...)
}

func installKubectl() {
	fmt.Println("Installing kubectl...")

	// Base URL for kubectl binaries
	baseURL := "https://hpcgame.pku.edu.cn/oss/images/public/sshop"

	// Determine download URL and binary name based on OS/arch
	var downloadURL, binaryName string
	switch runtime.GOOS {
	case "windows":
		downloadURL = baseURL + "/kubectl.exe"
		binaryName = "kubectl.exe"
	case "darwin":
		if runtime.GOARCH == "arm64" {
			downloadURL = baseURL + "/kubectl-arm64-darwin"
		} else {
			downloadURL = baseURL + "/kubectl-amd64-darwin"
		}
		binaryName = "kubectl"
	case "linux":
		downloadURL = baseURL + "/kubectl-amd64-linux"
		binaryName = "kubectl"
	default:
		fmt.Printf("Unsupported operating system: %s\n", runtime.GOOS)
		return
	}

	// Get install directory (~/.hpcgame/)
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("Failed to get home directory: %s\n", err)
		return
	}
	installDir := filepath.Join(homeDir, ".hpcgame")
	kubectlPath := filepath.Join(installDir, binaryName)

	// Create directory if not exists
	if err := os.MkdirAll(installDir, 0755); err != nil {
		fmt.Printf("Failed to create directory: %s\n", err)
		return
	}

	// Download kubectl
	fmt.Printf("Downloading kubectl from %s...\n", downloadURL)
	resp, err := http.Get(downloadURL)
	if err != nil {
		fmt.Printf("Failed to download kubectl: %s\n", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Failed to download kubectl: HTTP %d\n", resp.StatusCode)
		return
	}

	outFile, err := os.Create(kubectlPath)
	if err != nil {
		fmt.Printf("Failed to create file: %s\n", err)
		return
	}

	_, err = io.Copy(outFile, resp.Body)
	outFile.Close()
	if err != nil {
		fmt.Printf("Failed to write kubectl: %s\n", err)
		return
	}

	// Make executable on Unix systems
	if runtime.GOOS != "windows" {
		if err := os.Chmod(kubectlPath, 0755); err != nil {
			fmt.Printf("Failed to make kubectl executable: %s\n", err)
			return
		}
	}

	fmt.Printf("✅ kubectl downloaded to %s\n", kubectlPath)

	// Add to PATH
	switch runtime.GOOS {
	case "windows":
		// Add to user PATH via PowerShell
		addPathCmd := exec.Command("powershell", "-Command",
			fmt.Sprintf(`$currentPath = [Environment]::GetEnvironmentVariable('PATH', 'User'); `+
				`if ($currentPath -notlike '*%s*') { `+
				`[Environment]::SetEnvironmentVariable('PATH', $currentPath + ';%s', 'User'); `+
				`Write-Host 'Added to PATH. Please restart your terminal.' `+
				`} else { Write-Host 'Already in PATH.' }`, installDir, installDir))
		addPathCmd.Stdout = os.Stdout
		addPathCmd.Stderr = os.Stderr
		addPathCmd.Run()

	case "darwin", "linux":
		// Check if already in PATH
		currentPath := os.Getenv("PATH")
		if strings.Contains(currentPath, installDir) {
			fmt.Println("Already in PATH.")
		} else {
			// Add to shell config files
			shellConfigs := []string{
				filepath.Join(homeDir, ".bashrc"),
				filepath.Join(homeDir, ".zshrc"),
				filepath.Join(homeDir, ".bash_profile"),
			}
			pathLine := fmt.Sprintf("\nexport PATH=\"$PATH:%s\"\n", installDir)
			added := false
			for _, configFile := range shellConfigs {
				if _, err := os.Stat(configFile); err == nil {
					f, err := os.OpenFile(configFile, os.O_APPEND|os.O_WRONLY, 0644)
					if err == nil {
						f.WriteString(pathLine)
						f.Close()
						fmt.Printf("Added to %s\n", configFile)
						added = true
					}
				}
			}
			if added {
				fmt.Println("Please restart your terminal or run: source ~/.bashrc")
			} else {
				fmt.Printf("Please add %s to your PATH manually\n", installDir)
			}
			// Update current session
			os.Setenv("PATH", currentPath+":"+installDir)
		}
	}

	fmt.Println("✅ kubectl installed successfully")
}

func listPartitions(partitions []Partition) {
	fmt.Println("Available partitions:")
	fmt.Println("------------------------------------------------")
	for i, partition := range partitions {
		partitionDisplay := partition.GetDisplayName()
		if partition.Name != "" && partition.Name != partition.DisplayName {
			partitionDisplay = fmt.Sprintf("%s (%s)", partitionDisplay, partition.Name)
		}
		info := fmt.Sprintf("[%d] Partition: %s\n\tDescription: %s\n\tCPU Limit: %d\n\tMemory Limit: %dGiB\n",
			i, partitionDisplay, partition.Description, partition.CPULimit, partition.MemoryLimit)
		if partition.GPUTag != "" {
			info += fmt.Sprintf("\tAvailable GPU: %s\n", partition.GPUName)
		}
		info += "\tVerified images (custom images also supported):"
		for j, image := range partition.Images {
			info += fmt.Sprintf("\n\t\t[%d] %s", j, image)
		}
		fmt.Println(info)
		fmt.Println("------------------------------------------------")
	}
}

func listImages() {
	partitions := getPartitions()
	if partitions == nil {
		fmt.Println("❌ Failed to get partition information")
		return
	}

	fmt.Println("Available images by partition:")
	fmt.Println("------------------------------------------------")
	for _, partition := range partitions {
		fmt.Printf("Partition: %s\n", partition.GetDisplayName())
		for _, image := range partition.Images {
			fmt.Printf("  %s\n", image)
		}
		fmt.Println("------------------------------------------------")
	}
	fmt.Println("Note: Custom images are also supported if compatible with the partition")
}

func shellContainer() {
	kubeconfigPath := getKubeConfig()
	if kubeconfigPath == "" {
		return
	}

	if len(os.Args) < 3 {
		fmt.Println("Please specify the container to connect to")
		fmt.Println("Usage: hpcgame shell <container-name>")
		return
	}

	containerName := os.Args[2]
	fmt.Printf("Connecting to container %s...\n", containerName)

	cmd := kubectlCommand( "--kubeconfig", kubeconfigPath, "exec", "-it", containerName, "--", "/bin/bash")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("Failed to connect to container: %s\n", err)
		return
	}
}

func execInContainer() {
	kubeconfigPath := getKubeConfig()
	if kubeconfigPath == "" {
		return
	}

	// Parse command line options for exec
	interactive := false
	tty := false
	args := os.Args[2:]

	// Parse flags for interactive and tty options
	for i := 0; i < len(args); i++ {
		if args[i] == "-i" || args[i] == "--interactive" {
			interactive = true
			args = append(args[:i], args[i+1:]...)
			i--
		} else if args[i] == "-t" || args[i] == "--tty" {
			tty = true
			args = append(args[:i], args[i+1:]...)
			i--
		} else if args[i] == "-it" || args[i] == "-ti" {
			interactive = true
			tty = true
			args = append(args[:i], args[i+1:]...)
			i--
		}
	}

	if len(args) < 1 {
		fmt.Println("Container name required")
		fmt.Println("Usage: hpcgame exec [OPTIONS] CONTAINER COMMAND [ARG...]")
		fmt.Println("Options:")
		fmt.Println("  -i, --interactive    Keep STDIN open even if not attached")
		fmt.Println("  -t, --tty            Allocate a pseudo-TTY")
		return
	}

	containerName := args[0]
	cmdArgs := args[1:]

	// Default to bash shell if no command specified
	if len(cmdArgs) == 0 {
		cmdArgs = []string{"/bin/bash"}
	}

	fmt.Printf("Executing in container %s: %s\n", containerName, strings.Join(cmdArgs, " "))

	// Build kubectl command
	kubectlArgs := []string{"--kubeconfig", kubeconfigPath, "exec"}
	if interactive && tty {
		kubectlArgs = append(kubectlArgs, "-it")
	} else {
		if interactive {
			kubectlArgs = append(kubectlArgs, "-i")
		}
		if tty {
			kubectlArgs = append(kubectlArgs, "-t")
		}
	}
	kubectlArgs = append(kubectlArgs, containerName, "--")
	kubectlArgs = append(kubectlArgs, cmdArgs...)

	cmd := kubectlCommand( kubectlArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("Failed to execute command: %s\n", err)
		return
	}
}

func copyFiles() {
	kubeconfigPath := getKubeConfig()
	if kubeconfigPath == "" {
		return
	}

	if len(os.Args) < 4 {
		fmt.Println("Source and destination required")
		fmt.Println("Usage: hpcgame cp SOURCE DEST")
		fmt.Println("Examples:")
		fmt.Println("  hpcgame cp ./local-file.txt container:/path/to/file.txt")
		fmt.Println("  hpcgame cp container:/path/to/file.txt ./local-copy.txt")
		return
	}

	source := os.Args[2]
	destination := os.Args[3]

	// Handle containers with missing paths
	if strings.Contains(destination, ":") && strings.HasSuffix(destination, ":") {
		containerName := strings.TrimSuffix(destination, ":")
		destination = containerName + ":/partition-data"
		fmt.Printf("⚠️ No destination path specified, copying to home directory of container %s\n", containerName)
	}

	// Check for invalid source path
	if strings.Contains(source, ":") && strings.HasSuffix(source, ":") {
		fmt.Println("❌ Error: Source file path cannot be empty")
		fmt.Println("Usage: hpcgame cp SOURCE DEST")
		return
	}

	fmt.Printf("Copying: %s -> %s\n", source, destination)

	cmd := kubectlCommand( "--kubeconfig", kubeconfigPath, "cp", source, destination)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("Failed to copy files: %s\n", err)
		return
	}

	fmt.Println("✅ File copied successfully")
}

func portForward() {
	kubeconfigPath := getKubeConfig()
	if kubeconfigPath == "" {
		return
	}

	if len(os.Args) < 3 {
		fmt.Println("Container name and port mapping required")
		fmt.Println("Usage: hpcgame port CONTAINER LOCAL_PORT:CONTAINER_PORT")
		fmt.Println("Example: hpcgame port my-container 8080:80")
		return
	}

	containerName := os.Args[2]
	var portMapping string

	if len(os.Args) > 3 {
		portMapping = os.Args[3]
	} else {
		// Check if container name contains port mapping
		parts := strings.Split(containerName, " ")
		if len(parts) > 1 {
			containerName = parts[0]
			portMapping = parts[1]
		} else {
			fmt.Println("Port mapping required")
			fmt.Println("Usage: hpcgame port CONTAINER LOCAL_PORT:CONTAINER_PORT")
			return
		}
	}

	fmt.Printf("Setting up port forwarding: %s %s\n", containerName, portMapping)
	fmt.Println("Press Ctrl+C to stop forwarding")

	cmd := kubectlCommand( "--kubeconfig", kubeconfigPath, "port-forward", "pod/"+containerName, portMapping)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("Failed to set up port forwarding: %s\n", err)
		return
	}
}

func getPartitions() []Partition {
	// Get the user's home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("Failed to get user home directory: %s\n", err)
		return nil
	}

	// Create ~/.hpcgame directory if it doesn't exist
	hpcgameDir := filepath.Join(homeDir, kubeconfigDir)
	if _, err := os.Stat(hpcgameDir); os.IsNotExist(err) {
		err := os.MkdirAll(hpcgameDir, 0700)
		if err != nil {
			fmt.Printf("Failed to create directory: %s\n", err)
			return nil
		}
		fmt.Printf("Created HPCGame directory: %s\n", hpcgameDir)
	}

	partitionFile := filepath.Join(hpcgameDir, "partitions.json")
	lastUpdateFile := filepath.Join(hpcgameDir, "partition_last_update")
	if _, err := os.Stat(lastUpdateFile); os.IsNotExist(err) {
		// Create last update file
		os.WriteFile(lastUpdateFile, []byte("0"), 0644)
	}

	// Check if partitions need to be updated
	needsUpdate := true
	lastUpdateTime := 0
	lastUpdate, err := os.ReadFile(lastUpdateFile)
	if err == nil {
		lastUpdateTime, err = strconv.Atoi(string(lastUpdate))
		if err == nil && lastUpdateTime+86400 > int(time.Now().Unix()) {
			needsUpdate = false
		}
	}

	if needsUpdate {
		// Update partitions from the server
		resp, err := http.Get("https://hpcgame.pku.edu.cn/oss/images/public/partitions.json")
		if err != nil {
			fmt.Printf("Failed to get partition information: %s\n", err)
			return nil
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			fmt.Printf("Failed to get partition information: %s\n", resp.Status)
			return nil
		}

		// Save the updated partition information
		out, err := os.Create(partitionFile)
		if err != nil {
			fmt.Printf("Failed to create file: %s\n", err)
			return nil
		}
		defer out.Close()
		_, err = io.Copy(out, resp.Body)
		if err != nil {
			fmt.Printf("Failed to save partition information: %s\n", err)
			return nil
		}

		// Update last update timestamp
		err = os.WriteFile(lastUpdateFile, []byte(strconv.Itoa(int(time.Now().Unix()))), 0644)
		if err != nil {
			fmt.Printf("Failed to update partition timestamp: %s\n", err)
			return nil
		}

		fmt.Printf("Partition information updated: %s\n", partitionFile)
	}

	// Read partitions from file
	data, err := os.ReadFile(partitionFile)
	if err != nil {
		fmt.Printf("Failed to read partition information: %s\n", err)
		return nil
	}

	var partitions []Partition
	err = json.Unmarshal(data, &partitions)
	if err != nil {
		fmt.Printf("Failed to parse partition information: %s\n", err)
		return nil
	}

	return partitions
}

func getKubeconfigFromUser() string {
	fmt.Println("Please enter your kubeconfig content. You can get it from https://hpcgame.pku.edu.cn/kube/_/ui/#/tokens/")
	fmt.Println("Press Enter twice (two empty lines) when finished:")

	var kubeconfig strings.Builder
	scanner := bufio.NewScanner(os.Stdin)
	emptyLineCount := 0

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			emptyLineCount++
			if emptyLineCount >= 2 {
				break // Two consecutive empty lines, end input
			}
		} else {
			emptyLineCount = 0 // Reset counter
		}
		kubeconfig.WriteString(line)
		kubeconfig.WriteString("\n")
	}

	return kubeconfig.String()
}

func validateKubeconfig(kubeconfig string) bool {
	// Create temporary file for kubeconfig
	tmpFile, err := os.CreateTemp("", "kubeconfig-*")
	if err != nil {
		fmt.Printf("Failed to create temporary file: %s\n", err)
		return false
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(kubeconfig); err != nil {
		fmt.Printf("Failed to write to temporary file: %s\n", err)
		return false
	}
	tmpFile.Close()

	// Validate kubeconfig by trying to list nodes
	cmd := kubectlCommand( "--kubeconfig", tmpFile.Name(), "get", "nodes")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()

	if err != nil {
		fmt.Printf("Kubeconfig validation failed: %s\n%s\n", err, stderr.String())
		return false
	}

	fmt.Println("✅ Kubeconfig validated successfully")
	return true
}

func saveKubeconfig(kubeconfig string) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("Failed to get user home directory: %s\n", err)
		return
	}

	configDir := filepath.Join(homeDir, kubeconfigDir)
	err = os.MkdirAll(configDir, 0700)
	if err != nil {
		fmt.Printf("Failed to create config directory: %s\n", err)
		return
	}

	kubeconfigPath := filepath.Join(configDir, kubeconfigFile)

	// Check if file already exists
	if _, err := os.Stat(kubeconfigPath); err == nil {
		fmt.Printf("Kubeconfig file already exists at %s\n", kubeconfigPath)
		fmt.Print("Overwrite? (y/n): ")

		reader := bufio.NewReader(os.Stdin)
		response, err := reader.ReadString('\n')
		if err != nil {
			fmt.Printf("Failed to read input: %s\n", err)
			return
		}

		response = strings.TrimSpace(strings.ToLower(response))
		if response != "y" && response != "yes" {
			fmt.Println("Operation cancelled")
			return
		}
	}

	// Write kubeconfig
	err = os.WriteFile(kubeconfigPath, []byte(kubeconfig), 0600)
	if err != nil {
		fmt.Printf("Failed to save kubeconfig: %s\n", err)
		return
	}

	fmt.Printf("✅ Kubeconfig saved to %s\n", kubeconfigPath)
}

func installVSCodeExtensions() {
	// Check if the 'code' command is available
	if _, err := exec.LookPath("code"); err != nil {
		fmt.Println("⚠️ VSCode command-line tool 'code' not found")
		fmt.Println("If you have VSCode installed, ensure the 'code' command is in your PATH")
		fmt.Println("Or manually install these VSCode extensions:")
		fmt.Println("- ms-kubernetes-tools.vscode-kubernetes-tools")
		fmt.Println("- ms-vscode-remote.remote-containers")
		return
	}

	extensions := []string{
		"ms-kubernetes-tools.vscode-kubernetes-tools",
		"ms-vscode-remote.remote-containers",
	}

	fmt.Println("Installing VSCode extensions...")

	for _, ext := range extensions {
		cmd := exec.Command("code", "--install-extension", ext)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()

		if err != nil {
			fmt.Printf("Failed to install extension %s: %s\n%s\n", ext, err, stderr.String())
		} else {
			fmt.Printf("✅ Installed extension %s\n", ext)
		}
	}
}

func getKubeConfig() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("Failed to get user home directory: %s\n", err)
		return ""
	}

	kubeconfigPath := filepath.Join(homeDir, kubeconfigDir, kubeconfigFile)
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		fmt.Printf("Kubeconfig not found: %s\n", kubeconfigPath)
		fmt.Println("Please run 'hpcgame install' first")
		return ""
	}

	return kubeconfigPath
}

func runContainer() {
	kubeconfigPath := getKubeConfig()
	if kubeconfigPath == "" {
		return
	}

	// Create new flag set for run command
	runCmd := flag.NewFlagSet("run", flag.ExitOnError)

	// Define command line options
	partitionFlag := runCmd.String("partition", "", "Specify partition name")
	cpuFlag := runCmd.Int("cpu", 1, "Specify CPU cores")
	memoryFlag := runCmd.Int("memory", 0, "Specify memory size in GiB")
	gpuFlag := runCmd.Int("gpu", 0, "Specify GPU count")
	nameFlag := runCmd.String("name", "", "Specify container name")
	volumeFlag := runCmd.String("volume", "", "Mount volumes (comma-separated)")
	runCmd.StringVar(volumeFlag, "volumes", "", "Mount volumes (alias)")
	imageFlag := runCmd.String("image", "", "Specify container image")
	durationFlag := runCmd.Int64("duration", 7200, "Pod maximum runtime in seconds (default: 7200, i.e., 2 hours)")
	helpFlag := runCmd.Bool("help", false, "Show help information")

	// Add short flags
	runCmd.StringVar(partitionFlag, "p", "", "Specify partition name (short)")
	runCmd.IntVar(cpuFlag, "c", 1, "Specify CPU cores (short)")
	runCmd.IntVar(memoryFlag, "m", 0, "Specify memory size in GiB (short)")
	runCmd.IntVar(gpuFlag, "g", 0, "Specify GPU count (short)")
	runCmd.StringVar(nameFlag, "n", "", "Specify container name (short)")
	runCmd.StringVar(volumeFlag, "v", "", "Mount volumes (short)")
	runCmd.StringVar(imageFlag, "i", "", "Specify container image (short)")
	runCmd.Int64Var(durationFlag, "d", 7200, "Pod maximum runtime in seconds (short)")
	runCmd.BoolVar(helpFlag, "h", false, "Show help information (short)")

	// Parse arguments
	if len(os.Args) < 3 {
		fmt.Println("Usage: hpcgame run [OPTIONS] [IMAGE]")
		runCmd.PrintDefaults()
		return
	}

	// Find the position where options end and the image begins
	imagePos := 0
	for i := 2; i < len(os.Args); i++ {
		if !strings.HasPrefix(os.Args[i], "-") {
			imagePos = i
			break
		}
	}

	var flagArgs []string
	var imageArg string

	if imagePos > 0 {
		flagArgs = os.Args[2:imagePos]
		imageArg = os.Args[imagePos]
	} else {
		flagArgs = os.Args[2:]
	}

	err := runCmd.Parse(flagArgs)
	if err != nil {
		fmt.Printf("Failed to parse arguments: %s\n", err)
		return
	}

	// Show help
	if *helpFlag {
		fmt.Println("Usage: hpcgame run [OPTIONS] [IMAGE]")
		fmt.Println("Options:")
		runCmd.PrintDefaults()
		return
	}

	// Get partitions
	partitions := getPartitions()
	if partitions == nil {
		fmt.Println("Failed to get partition information")
		return
	}

	// Handle partition
	partitionInput := *partitionFlag

	// If partition not provided, prompt interactively
	if partitionInput == "" {
		listPartitions(partitions)
		fmt.Print("Enter partition name: ")
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			partitionInput = scanner.Text()
		} else {
			fmt.Println("Failed to read input")
			return
		}
	}

	// Validate partition (supports both Name and DisplayName)
	partitionStruct, found := findPartition(partitions, partitionInput)
	if !found {
		fmt.Printf("Invalid partition: %s\n", partitionInput)
		listPartitions(partitions)
		return
	}

	// Handle CPU
	cpu := *cpuFlag
	if cpu <= 0 || cpu > partitionStruct.CPULimit {
		fmt.Printf("Invalid CPU value: %d, partition limit: %d\n", cpu, partitionStruct.CPULimit)
		return
	}

	// Handle memory
	memory := *memoryFlag
	if memory == 0 {
		// Default memory is CPU × 2
		memory = cpu * 2
		fmt.Printf("Memory not specified, using default: %dGiB\n", memory)
	}

	if memory <= 0 || memory > partitionStruct.MemoryLimit {
		fmt.Printf("Invalid memory value: %dGiB, partition limit: %dGiB\n", memory, partitionStruct.MemoryLimit)
		return
	}

	// Handle GPU
	gpu := *gpuFlag

	// Handle image - give priority to --image flag over positional argument
	image := *imageFlag
	if image == "" {
		image = imageArg
	}

	if image == "" {
		if len(partitionStruct.Images) > 0 {
			image = partitionStruct.Images[0]
			fmt.Printf("Image not specified, using default: %s\n", image)
		} else {
			fmt.Println("Partition has no default images, please specify an image")
			fmt.Print("Enter image name: ")
			scanner := bufio.NewScanner(os.Stdin)
			if scanner.Scan() {
				image = scanner.Text()
			} else {
				fmt.Println("Failed to read input")
				return
			}
		}
	}

	// Handle volumes
	var extraVolumes []string
	if *volumeFlag != "" {
		extraVolumes = strings.Split(*volumeFlag, ",")
		// Trim whitespace
		for i, vol := range extraVolumes {
			extraVolumes[i] = strings.TrimSpace(vol)
		}

		// Check if volumes exist
		for _, vol := range extraVolumes {
			cmd := kubectlCommand( "--kubeconfig", kubeconfigPath, "get", "pvc", vol)
			err := cmd.Run()
			if err != nil {
				fmt.Printf("Warning: Volume %s may not exist. Use 'hpcgame volume ls' to list available volumes\n", vol)
				fmt.Print("Continue anyway? (y/n): ")
				scanner := bufio.NewScanner(os.Stdin)
				if scanner.Scan() {
					response := strings.ToLower(scanner.Text())
					if response != "y" && response != "yes" {
						fmt.Println("Operation cancelled")
						return
					}
				}
			}
		}
	}

	// Handle container name
	name := *nameFlag
	if name == "" {
		name = fmt.Sprintf("container-%d", os.Getpid())
	}

	// Handle duration
	duration := *durationFlag
	if duration > 86400 {
		fmt.Printf("Duration %d exceeds maximum (86400 seconds = 1 day), setting to 86400\n", duration)
		duration = 86400
	}
	if duration <= 0 {
		fmt.Println("Duration must be positive, using default: 7200")
		duration = 7200
	}

	// Create container
	fmt.Printf("Creating container %s...\n", name)
	createErr := deployContainer(kubeconfigPath, partitionStruct, name, cpu, memory, gpu, image, extraVolumes, duration)
	if createErr != nil {
		fmt.Printf("Failed to create container: %s\n", createErr)
		return
	}

	// Wait for container to start
	fmt.Print("Waiting for container to start...")
	for i := 0; i < 10; i++ {
		fmt.Print(".")
		cmd := kubectlCommand( "--kubeconfig", kubeconfigPath, "get", "pod", name, "-o", "jsonpath={.status.phase}")
		output, err := cmd.Output()
		if err == nil && string(output) == "Running" {
			fmt.Println("\n✅ Container is running!")
			break
		}
		time.Sleep(2 * time.Second)
	}
	fmt.Println()

	// Print information about the container
	fmt.Printf("Container %s is ready\n", name)
	fmt.Printf("Partition: %s, CPUs: %d, Memory: %dGiB", partitionStruct.GetDisplayName(), cpu, memory)
	if gpu > 0 {
		fmt.Printf(", GPUs: %d", gpu)
	}
	fmt.Println()

	// Show how to access the container using both original and docker-style commands
	fmt.Println("\nYou can access the container with:")
	fmt.Printf("  hpcgame shell %s           (original command)\n", name)
	fmt.Printf("  hpcgame exec -it %s bash   (docker-style command)\n", name)

	// Print volume mount information
	fmt.Println("\nVolume mounts:")
	fmt.Printf("  - Partition default volume mounted at /partition-data (default working directory)\n")
	for _, vol := range extraVolumes {
		fmt.Printf("  - Volume '%s' mounted at /mnt/%s\n", vol, vol)
	}
}

func deployContainer(kubeconfigPath string, partition Partition, name string, cpu int, memory int, gpu int, image string, extraVolumes []string, duration int64) error {
	gpulimit := ""
	if gpu > 0 {
		gpulimit = fmt.Sprintf("%s: %d", partition.GPUTag, gpu)
	}

	err := ensurePartitionDefaultVolume(kubeconfigPath, partition)
	if err != nil {
		fmt.Printf("Warning: Unable to create default volume: %s\n", err)
		// Continue without mounting default volume
	}

	// Use StorageClass-based PVC name to allow sharing between partitions with same StorageClass
	defaultVolumeName := fmt.Sprintf("%s-default-pvc", partition.StorageClass)

	volumeMountsStr := `    volumeMounts:
    - name: default-data-volume
      mountPath: /partition-data
`
	volumesStr := `  volumes:
  - name: default-data-volume
    persistentVolumeClaim:
      claimName: ` + defaultVolumeName + `
`

	for i, volumeName := range extraVolumes {
		mountName := fmt.Sprintf("extra-volume-%d", i)
		volumeMountsStr += fmt.Sprintf("    - name: %s\n      mountPath: /mnt/%s\n", mountName, volumeName)
		volumesStr += fmt.Sprintf("  - name: %s\n    persistentVolumeClaim:\n      claimName: %s\n", mountName, volumeName)
	}

	// Generate YAML config
	yamlConfig := fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %s
spec:
  activeDeadlineSeconds: %d
  nodeSelector:
    hpc.lcpu.dev/partition: %s
  containers:
  - name: container
    securityContext:
      capabilities:
        add: ["SYS_PTRACE", "IPC_LOCK"]
    image: %s
    command: ["sleep", "infinity"]
    workingDir: /partition-data
    resources:
      requests:
        cpu: %dm
        memory: %dGi
        %s
      limits:
        cpu: %dm
        memory: %dGi
        %s
%s
%s
  restartPolicy: Never
`, name, duration, partition.Name, image, cpu*1000, memory, gpulimit, cpu*1000, memory, gpulimit, volumeMountsStr, volumesStr)

	// Print YAML config in debug mode
	if os.Getenv("DEBUG") != "" {
		fmt.Printf("Generated YAML config:\n%s\n", yamlConfig)
	}

	// Create temporary file
	tmpFile, err := os.CreateTemp("", "pod-*.yaml")
	if err != nil {
		return fmt.Errorf("failed to create temporary file: %s", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(yamlConfig); err != nil {
		return fmt.Errorf("failed to write to temporary file: %s", err)
	}
	tmpFile.Close()

	// Apply config
	cmd := kubectlCommand( "--kubeconfig", kubeconfigPath, "apply", "-f", tmpFile.Name())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()

	if err != nil {
		return fmt.Errorf("failed to deploy container: %s\n%s", err, stderr.String())
	}

	return nil
}

func listContainers() {
	kubeconfigPath := getKubeConfig()
	if kubeconfigPath == "" {
		return
	}

	fmt.Println("Retrieving container list...")

	// Get current namespace
	cmd := kubectlCommand( "--kubeconfig", kubeconfigPath, "config", "view", "--minify", "-o", "jsonpath={..namespace}")
	namespaceOutput, err := cmd.Output()

	namespace := string(namespaceOutput)
	if err != nil || namespace == "" {
		namespace = "default"
	}

	// Get pods as JSON to parse detailed status
	cmd = kubectlCommand( "--kubeconfig", kubeconfigPath, "get", "pods", "-n", namespace, "-o", "json")
	output, err := cmd.Output()
	if err != nil {
		fmt.Printf("Failed to get container list: %s\n", err)
		return
	}

	var podList struct {
		Items []struct {
			Metadata struct {
				Name              string `json:"name"`
				CreationTimestamp string `json:"creationTimestamp"`
			} `json:"metadata"`
			Spec struct {
				NodeName   string `json:"nodeName"`
				Containers []struct {
					Image string `json:"image"`
				} `json:"containers"`
			} `json:"spec"`
			Status struct {
				Phase  string `json:"phase"`
				Reason string `json:"reason"`
			} `json:"status"`
		} `json:"items"`
	}

	err = json.Unmarshal(output, &podList)
	if err != nil {
		fmt.Printf("Failed to parse container list: %s\n", err)
		return
	}

	// Print header
	fmt.Printf("%-20s %-45s %-10s %-22s %s\n", "CONTAINER", "IMAGE", "STATUS", "CREATED", "NODE")

	// Print each container
	for _, pod := range podList.Items {
		image := ""
		if len(pod.Spec.Containers) > 0 {
			image = pod.Spec.Containers[0].Image
		}

		// Determine display status
		status := pod.Status.Phase
		if pod.Status.Phase == "Failed" && pod.Status.Reason == "DeadlineExceeded" {
			status = "Timeout"
		}

		fmt.Printf("%-20s %-45s %-10s %-22s %s\n",
			pod.Metadata.Name,
			image,
			status,
			pod.Metadata.CreationTimestamp,
			pod.Spec.NodeName)
	}
}

func deleteContainer() {
	kubeconfigPath := getKubeConfig()
	if kubeconfigPath == "" {
		return
	}

	if len(os.Args) < 3 {
		fmt.Println("Container name required")
		fmt.Println("Usage: hpcgame rm CONTAINER")
		return
	}

	containerName := os.Args[2]
	fmt.Printf("Removing container %s...\n", containerName)

	cmd := kubectlCommand( "--kubeconfig", kubeconfigPath, "delete", "pod", containerName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("Failed to remove container: %s\n", err)
		return
	}

	fmt.Printf("✅ Container %s removed\n", containerName)
}

func ensurePartitionDefaultVolume(kubeconfigPath string, partition Partition) error {
	// Construct default volume name based on StorageClass
	// This allows partitions with the same StorageClass to share the same PVC
	defaultVolumeName := fmt.Sprintf("%s-default-pvc", partition.StorageClass)

	// Check if volume already exists
	cmd := kubectlCommand( "--kubeconfig", kubeconfigPath, "get", "pvc", defaultVolumeName)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	// If volume exists, return
	if err == nil {
		fmt.Printf("Default volume %s already exists\n", defaultVolumeName)
		return nil
	}

	// Get storage class name from partition config
	storageClassName := partition.StorageClass
	if storageClassName == "" {
		return fmt.Errorf("partition %s has no StorageClass configured", partition.Name)
	}

	// Create default volume (200Gi, ReadWriteMany)
	pvcYAML := fmt.Sprintf(`apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: %s
spec:
  storageClassName: %s
  accessModes:
    - ReadWriteMany
  resources:
    requests:
      storage: 200Gi
`, defaultVolumeName, storageClassName)

	// Create temporary file
	tmpFile, err := os.CreateTemp("", "pvc-*.yaml")
	if err != nil {
		return fmt.Errorf("failed to create temporary file: %s", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(pvcYAML); err != nil {
		return fmt.Errorf("failed to write to temporary file: %s", err)
	}
	tmpFile.Close()

	// Apply volume config
	cmd = kubectlCommand( "--kubeconfig", kubeconfigPath, "apply", "-f", tmpFile.Name())
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()

	if err != nil {
		return fmt.Errorf("failed to create default volume: %s\n%s", err, stderr.String())
	}

	fmt.Printf("✅ Default volume %s created (StorageClass: %s)\n", defaultVolumeName, storageClassName)
	return nil
}

func listVolumes(kubeconfigPath string) error {
	cmd := kubectlCommand( "--kubeconfig", kubeconfigPath, "get", "pvc", "-o", "json")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to get volume list: %s", err)
	}

	var pvcList struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				StorageClassName string `json:"storageClassName"`
				Resources        struct {
					Requests struct {
						Storage string `json:"storage"`
					} `json:"requests"`
				} `json:"resources"`
				AccessModes []string `json:"accessModes"`
			} `json:"spec"`
			Status struct {
				Phase string `json:"phase"`
			} `json:"status"`
		} `json:"items"`
	}

	err = json.Unmarshal(output, &pvcList)
	if err != nil {
		return fmt.Errorf("failed to parse volume list: %s", err)
	}

	fmt.Println("VOLUME LIST")
	fmt.Println("===============================================================================")
	fmt.Printf("%-25s %-15s %-20s %-15s %-10s %s\n", "NAME", "SIZE", "STORAGE CLASS", "ACCESS MODE", "STATUS", "NOTES")
	fmt.Println("-------------------------------------------------------------------------------")

	for _, pvc := range pvcList.Items {
		isDefault := strings.Contains(pvc.Metadata.Name, "-default-pvc")
		accessModes := strings.Join(pvc.Spec.AccessModes, ",")
		notes := ""
		if isDefault {
			notes = "Default volume (cannot be removed)"
		}

		fmt.Printf("%-25s %-15s %-20s %-15s %-10s %s\n",
			pvc.Metadata.Name,
			pvc.Spec.Resources.Requests.Storage,
			pvc.Spec.StorageClassName,
			accessModes,
			pvc.Status.Phase,
			notes)
	}
	fmt.Println("===============================================================================")
	return nil
}

func createContainer() {
	kubeconfigPath := getKubeConfig()
	if kubeconfigPath == "" {
		return
	}

	// Create new flag set for create command
	createCmd := flag.NewFlagSet("create", flag.ExitOnError)

	// Define command line options
	partitionFlag := createCmd.String("partition", "", "Specify partition name")
	cpuFlag := createCmd.Int("cpu", 0, "Specify CPU cores")
	memoryFlag := createCmd.Int("memory", 0, "Specify memory size in GiB")
	gpuFlag := createCmd.Int("gpu", 0, "Specify GPU count")
	imageFlag := createCmd.String("image", "", "Specify container image")
	nameFlag := createCmd.String("name", "", "Specify container name")
	volumesFlag := createCmd.String("volumes", "", "Specify additional volumes to mount (comma-separated)")
	createCmd.StringVar(volumesFlag, "volume", "", "Specify additional volumes (alias)")
	durationFlag := createCmd.Int64("duration", 7200, "Pod maximum runtime in seconds (default: 7200, i.e., 2 hours)")
	helpFlag := createCmd.Bool("help", false, "Show help information")

	// Add short flags
	createCmd.StringVar(partitionFlag, "p", "", "Specify partition name (short)")
	createCmd.IntVar(cpuFlag, "c", 0, "Specify CPU cores (short)")
	createCmd.IntVar(memoryFlag, "m", 0, "Specify memory size in GiB (short)")
	createCmd.IntVar(gpuFlag, "g", 0, "Specify GPU count (short)")
	createCmd.StringVar(imageFlag, "i", "", "Specify container image (short)")
	createCmd.StringVar(nameFlag, "n", "", "Specify container name (short)")
	createCmd.StringVar(volumesFlag, "v", "", "Specify additional volumes (short)")
	createCmd.Int64Var(durationFlag, "d", 7200, "Pod maximum runtime in seconds (short)")
	createCmd.BoolVar(helpFlag, "h", false, "Show help information (short)")

	// Parse arguments
	if len(os.Args) < 3 {
		fmt.Println("Usage: hpcgame create [OPTIONS]")
		createCmd.PrintDefaults()
		return
	}

	err := createCmd.Parse(os.Args[2:])
	if err != nil {
		fmt.Printf("Failed to parse arguments: %s\n", err)
		return
	}

	// Show help
	if *helpFlag {
		fmt.Println("Usage: hpcgame create [OPTIONS]")
		fmt.Println("Options:")
		createCmd.PrintDefaults()
		return
	}

	// Get partitions
	partitions := getPartitions()
	if partitions == nil {
		fmt.Println("Failed to get partition information")
		return
	}

	// Handle partition
	partitionInput := *partitionFlag

	// If partition not provided, prompt interactively
	if partitionInput == "" {
		listPartitions(partitions)
		fmt.Print("Enter partition name: ")
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			partitionInput = scanner.Text()
		} else {
			fmt.Println("Failed to read input")
			return
		}
	}

	// Validate partition (supports both Name and DisplayName)
	partitionStruct, found := findPartition(partitions, partitionInput)
	if !found {
		fmt.Printf("Invalid partition: %s\n", partitionInput)
		listPartitions(partitions)
		return
	}

	// Handle CPU
	cpu := *cpuFlag
	if cpu == 0 {
		fmt.Print("Enter CPU cores: ")
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			cpuValue := scanner.Text()
			parsedCPU, err := strconv.Atoi(cpuValue)
			if err != nil {
				fmt.Printf("Invalid CPU value: %s\n", cpuValue)
				return
			}
			cpu = parsedCPU
		} else {
			fmt.Println("Failed to read input")
			return
		}
	}

	if cpu <= 0 || cpu > partitionStruct.CPULimit {
		fmt.Printf("Invalid CPU value: %d, partition limit: %d\n", cpu, partitionStruct.CPULimit)
		return
	}

	// Handle memory
	memory := *memoryFlag
	if memory == 0 {
		// Default memory is CPU × 2
		memory = cpu * 2
		fmt.Printf("Memory not specified, using default: %dGiB\n", memory)
	}

	if memory <= 0 || memory > partitionStruct.MemoryLimit {
		fmt.Printf("Invalid memory value: %dGiB, partition limit: %dGiB\n", memory, partitionStruct.MemoryLimit)
		return
	}

	// Handle GPU
	gpu := *gpuFlag

	// Handle image
	image := *imageFlag
	if image == "" {
		if len(partitionStruct.Images) > 0 {
			image = partitionStruct.Images[0]
			fmt.Printf("Image not specified, using default: %s\n", image)
		} else {
			fmt.Println("Partition has no default images, please specify an image")
			fmt.Print("Enter image name: ")
			scanner := bufio.NewScanner(os.Stdin)
			if scanner.Scan() {
				image = scanner.Text()
			} else {
				fmt.Println("Failed to read input")
				return
			}
		}
	}

	// Handle volumes
	var extraVolumes []string
	if *volumesFlag != "" {
		extraVolumes = strings.Split(*volumesFlag, ",")
		// Trim whitespace
		for i, vol := range extraVolumes {
			extraVolumes[i] = strings.TrimSpace(vol)
		}

		// Check if volumes exist
		for _, vol := range extraVolumes {
			cmd := kubectlCommand( "--kubeconfig", kubeconfigPath, "get", "pvc", vol)
			err := cmd.Run()
			if err != nil {
				fmt.Printf("Warning: Volume %s may not exist. Use 'hpcgame volume ls' to list available volumes\n", vol)
				fmt.Print("Continue anyway? (y/n): ")
				scanner := bufio.NewScanner(os.Stdin)
				if scanner.Scan() {
					response := strings.ToLower(scanner.Text())
					if response != "y" && response != "yes" {
						fmt.Println("Operation cancelled")
						return
					}
				}
			}
		}
	}

	// Handle container name
	name := *nameFlag
	if name == "" {
		name = fmt.Sprintf("container-%d", os.Getpid())
	}

	// Handle duration
	duration := *durationFlag
	if duration > 86400 {
		fmt.Printf("Duration %d exceeds maximum (86400 seconds = 1 day), setting to 86400\n", duration)
		duration = 86400
	}
	if duration <= 0 {
		fmt.Println("Duration must be positive, using default: 7200")
		duration = 7200
	}

	// Create container
	fmt.Printf("Creating container %s...\n", name)
	createErr := deployContainer(kubeconfigPath, partitionStruct, name, cpu, memory, gpu, image, extraVolumes, duration)
	if createErr != nil {
		fmt.Printf("Failed to create container: %s\n", createErr)
		return
	}

	fmt.Printf("✅ Container %s creation request submitted\n", name)
	fmt.Printf("  - Default partition volume mounted to /partition-data (default working directory)\n")
	for _, vol := range extraVolumes {
		fmt.Printf("  - Volume '%s' mounted to /mnt/%s\n", vol, vol)
	}

	fmt.Println("\nYou can connect to the container once it's running with:")
	fmt.Printf("  hpcgame shell %s\n", name)
}

func createVolume(kubeconfigPath string, name string, size string, storageClass string, accessMode string) error {
	// Check if this is a default volume
	if strings.Contains(name, "-default-pvc") {
		return fmt.Errorf("cannot create volume with name containing '-default-pvc', this is a reserved format")
	}

	// Create volume YAML
	pvcYAML := fmt.Sprintf(`apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: %s
spec:
  storageClassName: %s
  accessModes:
    - %s
  resources:
    requests:
      storage: %s
`, name, storageClass, accessMode, size)

	// Create temporary file
	tmpFile, err := os.CreateTemp("", "pvc-*.yaml")
	if err != nil {
		return fmt.Errorf("failed to create temporary file: %s", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(pvcYAML); err != nil {
		return fmt.Errorf("failed to write to temporary file: %s", err)
	}
	tmpFile.Close()

	// Apply volume config
	cmd := kubectlCommand( "--kubeconfig", kubeconfigPath, "apply", "-f", tmpFile.Name())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()

	if err != nil {
		return fmt.Errorf("failed to create volume: %s\n%s", err, stderr.String())
	}

	fmt.Printf("✅ Volume %s created\n", name)
	return nil
}

func deleteVolume(kubeconfigPath string, name string) error {
	// Check if this is a default volume
	if strings.Contains(name, "-default-pvc") {
		return fmt.Errorf("cannot delete default volume: %s", name)
	}

	// Delete volume
	cmd := kubectlCommand( "--kubeconfig", kubeconfigPath, "delete", "pvc", name)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()

	if err != nil {
		return fmt.Errorf("failed to delete volume: %s\n%s", err, stderr.String())
	}

	fmt.Printf("✅ Volume %s deleted\n", name)
	return nil
}

func handleVolumeCommands() {
	if len(os.Args) < 3 {
		printVolumeHelp()
		return
	}

	kubeconfigPath := getKubeConfig()
	if kubeconfigPath == "" {
		return
	}

	subCommand := os.Args[2]

	switch subCommand {
	case "ls", "list":
		err := listVolumes(kubeconfigPath)
		if err != nil {
			fmt.Printf("Failed to list volumes: %s\n", err)
		}
	case "create":
		if len(os.Args) < 6 {
			fmt.Println("Insufficient parameters")
			fmt.Println("Usage: hpcgame volume create NAME SIZE STORAGE_CLASS [ACCESS_MODE]")
			fmt.Println("Example: hpcgame volume create my-data 10Gi x86-amd-default-sc ReadWriteMany")
			return
		}
		name := os.Args[3]
		size := os.Args[4]
		storageClass := os.Args[5]
		accessMode := "ReadWriteMany" // Default
		if len(os.Args) > 6 {
			accessMode = os.Args[6]
		}

		err := createVolume(kubeconfigPath, name, size, storageClass, accessMode)
		if err != nil {
			fmt.Printf("Failed to create volume: %s\n", err)
		}
	case "rm", "delete", "remove":
		if len(os.Args) < 4 {
			fmt.Println("Volume name required")
			fmt.Println("Usage: hpcgame volume rm NAME")
			fmt.Println("Example: hpcgame volume rm my-data")
			return
		}
		name := os.Args[3]
		err := deleteVolume(kubeconfigPath, name)
		if err != nil {
			fmt.Printf("Failed to delete volume: %s\n", err)
		}
	default:
		fmt.Printf("Unknown volume subcommand: %s\n", subCommand)
		printVolumeHelp()
	}
}

func printVolumeHelp() {
	helpText := `Volume command usage:
  hpcgame volume ls                                  List all volumes
  hpcgame volume create NAME SIZE STORAGE_CLASS [MODE]  Create a new volume
  hpcgame volume rm NAME                             Delete a volume

Examples:
  hpcgame volume ls
  hpcgame volume create my-data 10Gi x86-amd-default-sc ReadWriteMany
  hpcgame volume rm my-data
  
Note:
  - Default volumes (names containing '-default-pvc') cannot be deleted
  - If access mode is not specified, ReadWriteMany is used
  - Size must include units (e.g., Gi, Mi)
`
	fmt.Println(helpText)
}
