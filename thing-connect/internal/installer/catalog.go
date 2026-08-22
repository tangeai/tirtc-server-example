package installer

import (
	_ "embed"
	"encoding/csv"
	"fmt"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// serviceSpec is the installer-owned catalog for generated configs, strict
// bundle validation and Supervisor startup/readiness ordering. It is kept
// private so composition roots cannot duplicate service-selection policy.
type serviceSpec struct {
	Name        string
	HTTPPort    int
	Business    bool
	Required    bool
	UsesMQTT    bool
	StaticDir   string
	DisplayName string
}

//go:embed service_catalog.tsv
var serviceCatalogTSV string

var serviceCatalog = mustLoadServiceCatalog(serviceCatalogTSV)

var serviceNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*-server$`)

func publicServiceCatalog() []ServiceDefinition {
	result := make([]ServiceDefinition, 0, len(serviceCatalog))
	for _, service := range serviceCatalog {
		result = append(result, ServiceDefinition{
			Name: service.Name, DisplayName: service.DisplayName, Business: service.Business,
			Required: service.Required, UsesMQTT: service.UsesMQTT,
		})
	}
	return result
}

func adminService() serviceSpec {
	for _, service := range serviceCatalog {
		if !service.Business {
			return service
		}
	}
	panic("installer service catalog has no Admin service")
}

func optionalServicesSelected(optional []string, names ...string) bool {
	selected := make(map[string]bool, len(optional))
	for _, name := range optional {
		selected[name] = true
	}
	for _, name := range names {
		if !selected[name] {
			return false
		}
	}
	return true
}

func mustLoadServiceCatalog(raw string) []serviceSpec {
	reader := csv.NewReader(strings.NewReader(raw))
	reader.Comma = '\t'
	reader.Comment = '#'
	reader.FieldsPerRecord = 7
	reader.TrimLeadingSpace = true
	services := make([]serviceSpec, 0, 8)
	names, ports := map[string]bool{}, map[int]bool{}
	adminCount := 0
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			panic(fmt.Sprintf("invalid installer service catalog: %v", err))
		}
		name := strings.TrimSpace(record[0])
		port, err := strconv.Atoi(record[1])
		if err != nil || port < 1 || port > 65535 || !serviceNamePattern.MatchString(name) || names[name] || ports[port] {
			panic(fmt.Sprintf("invalid installer service catalog row for %q", record[0]))
		}
		required, requiredErr := strconv.ParseBool(record[3])
		usesMQTT, mqttErr := strconv.ParseBool(record[4])
		if requiredErr != nil || mqttErr != nil || (record[2] != "admin" && record[2] != "business") || strings.TrimSpace(record[6]) == "" {
			panic(fmt.Sprintf("invalid installer service catalog attributes for %q", record[0]))
		}
		staticDir := strings.TrimSpace(record[5])
		cleanStaticDir := path.Clean(staticDir)
		if staticDir != "-" && (path.IsAbs(staticDir) || cleanStaticDir == "." || cleanStaticDir == ".." || strings.HasPrefix(cleanStaticDir, "../")) {
			panic(fmt.Sprintf("invalid installer static directory for %q", name))
		}
		service := serviceSpec{
			Name: name, HTTPPort: port, Business: record[2] == "business", Required: required,
			UsesMQTT: usesMQTT, StaticDir: staticDir, DisplayName: strings.TrimSpace(record[6]),
		}
		if !service.Business {
			if service.Name != "admin-server" {
				panic("installer Admin service must be named admin-server")
			}
			adminCount++
			if !service.Required {
				panic("installer Admin service must be required")
			}
		}
		names[service.Name], ports[service.HTTPPort] = true, true
		services = append(services, service)
	}
	if adminCount != 1 || len(services) < 2 {
		panic("installer service catalog must contain one Admin and at least one business service")
	}
	return services
}

func installBusinessServices() []serviceSpec {
	result := make([]serviceSpec, 0, len(serviceCatalog)-1)
	for _, service := range serviceCatalog {
		if service.Business {
			result = append(result, service)
		}
	}
	return result
}

func installOptionalServices() []string {
	result := make([]string, 0, len(serviceCatalog))
	for _, service := range serviceCatalog {
		if service.Business && !service.Required {
			result = append(result, service.Name)
		}
	}
	return result
}

func businessServiceNames() []string {
	services := installBusinessServices()
	result := make([]string, 0, len(services))
	for _, service := range services {
		result = append(result, service.Name)
	}
	return result
}
