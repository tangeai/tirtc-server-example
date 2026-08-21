package installer

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

type SupervisorController struct {
	command   string
	group     string
	client    *http.Client
	timeout   time.Duration
	adminPort int
}

func NewSupervisorController(command, group string, adminPorts ...int) *SupervisorController {
	if strings.TrimSpace(command) == "" {
		command = "supervisorctl"
	}
	if strings.TrimSpace(group) == "" {
		group = "demo-open"
	}
	adminPort := 9000
	if len(adminPorts) > 0 && adminPorts[0] > 0 {
		adminPort = adminPorts[0]
	}
	return &SupervisorController{
		command: command, group: group,
		client: &http.Client{Timeout: 3 * time.Second}, timeout: 60 * time.Second, adminPort: adminPort,
	}
}

func (s *SupervisorController) StartAndWait(ctx context.Context, optional []string, progress func(ServiceState)) error {
	enabled, err := enabledBusinessServices(optional)
	if err != nil {
		return err
	}
	progress(ServiceState{Name: "admin-server", State: "checking"})
	if err := s.waitReady(ctx, "admin-server", s.adminPort); err != nil {
		progress(ServiceState{Name: "admin-server", State: "not_ready"})
		return err
	}
	progress(ServiceState{Name: "admin-server", State: "ready"})
	for _, service := range enabled {
		progress(ServiceState{Name: service.Name, State: "starting"})
		state, err := s.status(ctx, service.Name)
		if err != nil {
			progress(ServiceState{Name: service.Name, State: "failed"})
			return err
		}
		if state != "RUNNING" {
			if _, err := s.run(ctx, "start", s.program(service.Name)); err != nil {
				progress(ServiceState{Name: service.Name, State: "failed"})
				return fmt.Errorf("启动 %s 失败: %w", service.Name, err)
			}
		}
		if err := s.waitReady(ctx, service.Name, service.HTTPPort); err != nil {
			progress(ServiceState{Name: service.Name, State: "not_ready"})
			return err
		}
		progress(ServiceState{Name: service.Name, State: "ready"})
	}
	return nil
}

func (s *SupervisorController) status(ctx context.Context, service string) (string, error) {
	output, err := s.run(ctx, "status", s.program(service))
	if err != nil {
		return "", fmt.Errorf("读取 %s Supervisor 状态失败: %w", service, err)
	}
	fields := strings.Fields(output)
	if len(fields) < 2 {
		return "", fmt.Errorf("%s Supervisor 状态响应无效", service)
	}
	return fields[1], nil
}

func (s *SupervisorController) waitReady(ctx context.Context, service string, port int) error {
	deadline := time.NewTimer(s.timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	url := fmt.Sprintf("http://127.0.0.1:%d/health/ready", port)
	for {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		response, err := s.client.Do(request)
		if err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("%s 在 %s 内未通过 readiness", service, s.timeout)
		case <-ticker.C:
		}
	}
}

func (s *SupervisorController) program(service string) string {
	return s.group + ":" + service
}

func (s *SupervisorController) run(ctx context.Context, args ...string) (string, error) {
	command := exec.CommandContext(ctx, s.command, args...)
	raw, err := command.CombinedOutput()
	output := strings.TrimSpace(string(raw))
	if err != nil {
		return output, fmt.Errorf("%s", output)
	}
	return output, nil
}
