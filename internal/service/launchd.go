package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"
)

const (
	PlistLabel      = "com.gcalsync.sync"
	DefaultInterval = 15 * time.Minute
)

type LaunchdService struct {
	BinaryPath string
	Interval   time.Duration
	LogDir     string
}

type ServiceStatus struct {
	Installed bool
	Running   bool
	PID       int
	LastExit  int
}

func plistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", PlistLabel+".plist")
}

func defaultLogDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Logs", "gcalsync")
}

var plistTmpl = template.Must(template.New("plist").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>{{.Label}}</string>
    <key>ProgramArguments</key>
    <array>
        <string>{{.BinaryPath}}</string>
        <string>sync</string>
    </array>
    <key>StartInterval</key>
    <integer>{{.IntervalSeconds}}</integer>
    <key>StandardOutPath</key>
    <string>{{.StdoutPath}}</string>
    <key>StandardErrorPath</key>
    <string>{{.StderrPath}}</string>
    <key>RunAtLoad</key>
    <true/>
</dict>
</plist>
`))

type plistData struct {
	Label           string
	BinaryPath      string
	IntervalSeconds int
	StdoutPath      string
	StderrPath      string
}

func NewLaunchdService(interval time.Duration) (*LaunchdService, error) {
	binary, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolving binary path: %w", err)
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		return nil, fmt.Errorf("resolving absolute path: %w", err)
	}

	if interval == 0 {
		interval = DefaultInterval
	}

	return &LaunchdService{
		BinaryPath: binary,
		Interval:   interval,
		LogDir:     defaultLogDir(),
	}, nil
}

func (s *LaunchdService) Install() error {
	if err := os.MkdirAll(s.LogDir, 0o755); err != nil {
		return fmt.Errorf("creating log directory: %w", err)
	}

	path := plistPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating LaunchAgents directory: %w", err)
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating plist: %w", err)
	}
	defer f.Close()

	data := plistData{
		Label:           PlistLabel,
		BinaryPath:      s.BinaryPath,
		IntervalSeconds: int(s.Interval.Seconds()),
		StdoutPath:      filepath.Join(s.LogDir, "gcalsync.log"),
		StderrPath:      filepath.Join(s.LogDir, "gcalsync.err.log"),
	}

	if err := plistTmpl.Execute(f, data); err != nil {
		return fmt.Errorf("writing plist: %w", err)
	}

	if err := exec.Command("launchctl", "load", path).Run(); err != nil {
		return fmt.Errorf("loading plist: %w", err)
	}

	return nil
}

func (s *LaunchdService) Uninstall() error {
	path := plistPath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("service not installed")
	}

	exec.Command("launchctl", "unload", path).Run()
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("removing plist: %w", err)
	}
	return nil
}

func (s *LaunchdService) Status() (*ServiceStatus, error) {
	path := plistPath()
	status := &ServiceStatus{}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return status, nil
	}
	status.Installed = true

	out, err := exec.Command("launchctl", "list").Output()
	if err != nil {
		return status, nil
	}

	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, PlistLabel) {
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				if pid, err := strconv.Atoi(fields[0]); err == nil && fields[0] != "-" {
					status.Running = true
					status.PID = pid
				}
				if exit, err := strconv.Atoi(fields[1]); err == nil {
					status.LastExit = exit
				}
			}
			break
		}
	}

	return status, nil
}

func (s *LaunchdService) LogPath() string {
	return filepath.Join(s.LogDir, "gcalsync.log")
}
