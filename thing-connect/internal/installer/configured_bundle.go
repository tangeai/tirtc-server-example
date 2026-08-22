package installer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-sql-driver/mysql"

	adminapp "thing-connect/internal/admin"
	baseconfig "thing-connect/internal/config"
)

type configuredService struct {
	name        string
	databaseDSN string
}

// ValidateConfiguredServiceBundle strictly validates every service represented
// by the active config revision. Catalog and compatibility selection rules stay
// inside the installer module.
func ValidateConfiguredServiceBundle(root string) error {
	_, err := inspectConfiguredServiceBundle(root)
	return err
}

// ValidateConfiguredBusinessService validates exactly one catalog-owned
// business service without inspecting or changing any other service config.
func ValidateConfiguredBusinessService(root, serviceName string) error {
	for _, service := range serviceCatalog {
		if service.Name != serviceName {
			continue
		}
		if !service.Business {
			return fmt.Errorf("%s is not a business service", serviceName)
		}
		configuredPath := filepath.Join(root, service.Name, "config.yaml")
		if _, err := os.Lstat(baseconfig.ResolvePath(configuredPath)); err != nil {
			return fmt.Errorf("%s config is missing: %w", service.Name, err)
		}
		if _, err := baseconfig.LoadFile(configuredPath); err != nil {
			return fmt.Errorf("%s: %w", service.Name, err)
		}
		return nil
	}
	return fmt.Errorf("unknown business service %q", serviceName)
}

// ValidateConfiguredRuntimeTarget proves that the explicit migration account
// points at the same MySQL endpoint and schema as every selected runtime
// service. Credentials and connection parameters do not affect identity.
func ValidateConfiguredRuntimeTarget(root, migrationDSN string) error {
	want, err := databaseTargetIdentity(migrationDSN)
	if err != nil {
		return fmt.Errorf("migration database: %w", err)
	}
	services, err := inspectConfiguredServiceBundle(root)
	if err != nil {
		return err
	}
	for _, service := range services {
		got, identityErr := databaseTargetIdentity(service.databaseDSN)
		if identityErr != nil {
			return fmt.Errorf("%s runtime database: %w", service.name, identityErr)
		}
		if got != want {
			return fmt.Errorf("%s runtime database does not match migration target", service.name)
		}
	}
	return nil
}

func inspectConfiguredServiceBundle(root string) ([]configuredService, error) {
	result := make([]configuredService, 0, len(serviceCatalog))
	for _, service := range serviceCatalog {
		configuredPath := filepath.Join(root, service.Name, "config.yaml")
		resolvedPath := baseconfig.ResolvePath(configuredPath)
		if _, err := os.Lstat(resolvedPath); err != nil {
			if service.Required {
				return nil, fmt.Errorf("%s is required but its config is missing", service.Name)
			}
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("inspect %s config: %w", service.Name, err)
		}
		if service.Name == "admin-server" {
			cfg, err := adminapp.LoadAppConfig(configuredPath)
			if err != nil {
				return nil, fmt.Errorf("admin-server: %w", err)
			}
			result = append(result, configuredService{name: service.Name, databaseDSN: cfg.Database.DSN})
			continue
		}
		cfg, err := baseconfig.LoadFile(configuredPath)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", service.Name, err)
		}
		result = append(result, configuredService{name: service.Name, databaseDSN: cfg.Database.DSN})
	}
	return result, nil
}

func configuredOptionalServiceNames(root string) ([]string, error) {
	configured, err := inspectConfiguredServiceBundle(root)
	if err != nil {
		return nil, err
	}
	present := make(map[string]bool, len(configured))
	for _, service := range configured {
		present[service.name] = true
	}
	result := make([]string, 0, 3)
	for _, service := range serviceCatalog {
		if service.Business && !service.Required && present[service.Name] {
			result = append(result, service.Name)
		}
	}
	return result, nil
}

func databaseTargetIdentity(dsn string) (string, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return "", fmt.Errorf("解析 MySQL DSN 失败: %w", err)
	}
	if strings.TrimSpace(cfg.DBName) == "" {
		return "", fmt.Errorf("MySQL DSN 未选择数据库")
	}
	network := cfg.Net
	if network == "" {
		network = "tcp"
	}
	address := cfg.Addr
	if address == "" && network == "tcp" {
		address = "127.0.0.1:3306"
	}
	return network + "\x00" + address + "\x00" + cfg.DBName, nil
}
