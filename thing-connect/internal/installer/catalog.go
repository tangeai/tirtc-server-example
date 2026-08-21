package installer

import "fmt"

// serviceSpec is the installer-owned catalog for generated configs, strict
// bundle validation and Supervisor startup/readiness ordering. It is kept
// private so composition roots cannot duplicate service-selection policy.
type serviceSpec struct {
	Name        string
	HTTPPort    int
	Business    bool
	Required    bool
	UsesMQTT    bool
	DisplayName string
}

var serviceCatalog = []serviceSpec{
	{Name: "admin-server", HTTPPort: 9000, Required: true, DisplayName: "管理后台"},
	{Name: "device-server", HTTPPort: 9001, Business: true, Required: true, UsesMQTT: true, DisplayName: "设备服务"},
	{Name: "user-server", HTTPPort: 9002, Business: true, Required: true, UsesMQTT: true, DisplayName: "用户服务"},
	{Name: "voip-server", HTTPPort: 9003, Business: true, UsesMQTT: true, DisplayName: "VoIP 服务"},
	{Name: "ai-server", HTTPPort: 9004, Business: true, DisplayName: "AI 服务"},
	{Name: "call-server", HTTPPort: 9005, Business: true, UsesMQTT: true, DisplayName: "设备通话服务"},
}

func enabledServices(optional []string) ([]serviceSpec, error) {
	selected := make(map[string]bool, len(optional))
	for _, name := range optional {
		if selected[name] {
			return nil, fmt.Errorf("%w: 可选服务 %s 重复", ErrInvalidInput, name)
		}
		knownOptional := false
		for _, service := range serviceCatalog {
			if service.Name == name && service.Business && !service.Required {
				knownOptional = true
				break
			}
		}
		if !knownOptional {
			return nil, fmt.Errorf("%w: 未知的可选服务 %s", ErrInvalidInput, name)
		}
		selected[name] = true
	}
	result := make([]serviceSpec, 0, len(serviceCatalog))
	for _, service := range serviceCatalog {
		if service.Required || selected[service.Name] {
			result = append(result, service)
		}
	}
	return result, nil
}

func enabledBusinessServices(optional []string) ([]serviceSpec, error) {
	services, err := enabledServices(optional)
	if err != nil {
		return nil, err
	}
	result := make([]serviceSpec, 0, len(services))
	for _, service := range services {
		if service.Business {
			result = append(result, service)
		}
	}
	return result, nil
}

func enabledMQTTServices(optional []string) ([]serviceSpec, error) {
	services, err := enabledServices(optional)
	if err != nil {
		return nil, err
	}
	result := make([]serviceSpec, 0, len(services))
	for _, service := range services {
		if service.UsesMQTT {
			result = append(result, service)
		}
	}
	return result, nil
}

func canonicalOptionalServices(optional []string) ([]string, error) {
	services, err := enabledServices(optional)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(optional))
	for _, service := range services {
		if service.Business && !service.Required {
			result = append(result, service.Name)
		}
	}
	return result, nil
}
