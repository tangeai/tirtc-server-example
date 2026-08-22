package installer

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

type SupervisorController struct {
	command       string
	group         string
	client        *http.Client
	timeout       time.Duration
	adminPort     int
	portAvailable func(int) error
}

func NewSupervisorController(command, group string, adminPorts ...int) *SupervisorController {
	if strings.TrimSpace(command) == "" {
		command = "supervisorctl"
	}
	if strings.TrimSpace(group) == "" {
		group = "thing-connect"
	}
	adminPort := 9000
	if len(adminPorts) > 0 && adminPorts[0] > 0 {
		adminPort = adminPorts[0]
	}
	return &SupervisorController{
		command: command, group: group,
		client: &http.Client{Timeout: 3 * time.Second}, timeout: 60 * time.Second, adminPort: adminPort,
		portAvailable: tcpPortAvailable,
	}
}

func (s *SupervisorController) StartAndWait(ctx context.Context, optional []string, progress func(ServiceState)) error {
	enabled, err := enabledBusinessServices(optional)
	if err != nil {
		return err
	}
	progress(ServiceState{Name: "admin-server", State: "checking"})
	if err := s.waitReady(ctx, "admin-server", s.adminPort); err != nil {
		problem := serviceNotReadyProblem(serviceCatalog[0], s.timeout)
		progress(ServiceState{Name: "admin-server", State: "not_ready", Problem: &problem})
		return runtimeFailure(problem, err)
	}
	progress(ServiceState{Name: "admin-server", State: "ready"})
	for _, service := range enabled {
		progress(ServiceState{Name: service.Name, State: "starting"})
		state, err := s.status(ctx, service.Name)
		if err != nil {
			problem := serviceControllerProblem(service)
			progress(ServiceState{Name: service.Name, State: "failed", Problem: &problem})
			return runtimeFailure(problem, err)
		}
		if state != "RUNNING" {
			if err := s.portAvailable(service.HTTPPort); err != nil {
				problem := portInUseProblem(service)
				progress(ServiceState{Name: service.Name, State: "failed", Problem: &problem})
				return runtimeFailure(problem, err)
			}
			if _, err := s.run(ctx, "start", s.program(service.Name)); err != nil {
				problem := serviceStartProblem(service)
				progress(ServiceState{Name: service.Name, State: "failed", Problem: &problem})
				return runtimeFailure(problem, fmt.Errorf("启动 %s 失败: %w", service.Name, err))
			}
		}
		if err := s.waitReady(ctx, service.Name, service.HTTPPort); err != nil {
			problem := serviceNotReadyProblem(service, s.timeout)
			progress(ServiceState{Name: service.Name, State: "not_ready", Problem: &problem})
			return runtimeFailure(problem, err)
		}
		progress(ServiceState{Name: service.Name, State: "ready"})
	}
	return nil
}

func tcpPortAvailable(port int) error {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return err
	}
	return listener.Close()
}

func portInUseProblem(service serviceSpec) Problem {
	port := fmt.Sprintf("%d", service.HTTPPort)
	return Problem{
		Code:    "PORT_IN_USE",
		Message: fmt.Sprintf("%s无法启动：端口 %s 已被占用", service.DisplayName, port),
		Suggestions: []string{
			fmt.Sprintf("在服务器执行 ss -ltnp | grep ':%s' 确认占用进程", port),
			"停止占用端口的旧实例，或确保同一服务只由一个进程管理器启动，然后重试",
		},
	}
}

func serviceControllerProblem(service serviceSpec) Problem {
	return Problem{
		Code:    "SERVICE_CONTROLLER_FAILED",
		Message: fmt.Sprintf("无法读取%s的进程状态", service.DisplayName),
		Suggestions: []string{
			"确认本地服务脚本或进程管理器可执行，并检查其运行账号和目录权限",
			"修复进程管理器后返回此页重试",
		},
	}
}

func serviceStartProblem(service serviceSpec) Problem {
	return Problem{
		Code:    "SERVICE_START_FAILED",
		Message: fmt.Sprintf("%s启动失败", service.DisplayName),
		Suggestions: []string{
			fmt.Sprintf("检查 logs/%s.out.log 和 logs/%s.err.log 中的启动错误", service.Name, service.Name),
			"检查配置文件、运行账号、目录权限和端口占用后重试",
		},
	}
}

func serviceNotReadyProblem(service serviceSpec, timeout time.Duration) Problem {
	return Problem{
		Code:    "SERVICE_NOT_READY",
		Message: fmt.Sprintf("%s启动后在 %s 内未通过就绪检查", service.DisplayName, timeout),
		Suggestions: []string{
			fmt.Sprintf("检查 logs/%s.out.log 和 logs/%s.err.log 中的 MySQL、Redis、MQTT 或端口错误", service.Name, service.Name),
			"确认依赖可用并修复日志中的首个错误，然后返回此页继续安装",
		},
	}
}

func (s *SupervisorController) status(ctx context.Context, service string) (string, error) {
	output, err := s.run(ctx, "status", s.program(service))
	if err != nil {
		return "", fmt.Errorf("读取 %s 服务控制器状态失败: %w", service, err)
	}
	fields := strings.Fields(output)
	if len(fields) < 2 {
		return "", fmt.Errorf("%s 服务控制器状态响应无效", service)
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
