package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"cresto/internal/config"
)

type pidInfo struct {
	Port int `json:"port"`
	PID  int `json:"pid"`
}

func readPidFile() (*pidInfo, error) {
	data, err := os.ReadFile(config.PidPath())
	if err != nil {
		return nil, err
	}
	var info pidInfo
	if err := json.Unmarshal(data, &info); err == nil {
		return &info, nil
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return nil, err
	}
	return &pidInfo{PID: pid}, nil
}

func writePidFile(port, pid int) error {
	data, err := json.Marshal(pidInfo{Port: port, PID: pid})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(config.PidPath()), 0755); err != nil {
		return err
	}
	return os.WriteFile(config.PidPath(), data, 0644)
}

func isServerRunning(port int) bool {
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/health", port))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
