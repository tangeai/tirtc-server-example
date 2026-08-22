package migrate

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
)

type migrationVersion struct {
	Version int
	Paths   []string
}

var migrationFileName = regexp.MustCompile(`^([0-9]{3})_[a-z0-9_]+\.sql$`)

var migrationCatalog = mustLoadMigrationCatalog()

func mustLoadMigrationCatalog() map[string][]migrationVersion {
	paths := make([]string, 0, 16)
	err := fs.WalkDir(migrationSQL, "migrations", func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && path.Ext(filePath) == ".sql" {
			paths = append(paths, filePath)
		}
		return nil
	})
	if err != nil {
		panic(fmt.Sprintf("discover embedded migrations: %v", err))
	}
	catalog, err := buildMigrationCatalog(paths)
	if err != nil {
		panic(err)
	}
	return catalog
}

func buildMigrationCatalog(paths []string) (map[string][]migrationVersion, error) {
	grouped := map[string]map[int][]string{"core": {}, "admin": {}}
	for _, filePath := range paths {
		dir, fileName := path.Split(filePath)
		component := path.Base(path.Clean(dir))
		if component == "shared" {
			continue
		}
		versions, known := grouped[component]
		if !known {
			return nil, fmt.Errorf("migration directory %q is not a supported component", component)
		}
		match := migrationFileName.FindStringSubmatch(fileName)
		if match == nil {
			return nil, fmt.Errorf("migration file %q must use NNN_description.sql", filePath)
		}
		version, err := strconv.Atoi(match[1])
		if err != nil || version < 1 {
			return nil, fmt.Errorf("migration file %q has an invalid version", filePath)
		}
		versions[version] = append(versions[version], filePath)
	}

	result := make(map[string][]migrationVersion, len(grouped))
	for _, component := range []string{"core", "admin"} {
		versions := grouped[component]
		maximum := 0
		for version := range versions {
			if version > maximum {
				maximum = version
			}
		}
		if maximum == 0 {
			return nil, fmt.Errorf("migration component %s has no versions", component)
		}
		entries := make([]migrationVersion, 0, maximum)
		for version := 1; version <= maximum; version++ {
			files := append([]string(nil), versions[version]...)
			if len(files) == 0 {
				return nil, fmt.Errorf("migration component %s has a gap at version %d", component, version)
			}
			sort.Strings(files)
			entries = append(entries, migrationVersion{Version: version, Paths: files})
		}
		result[component] = entries
	}
	return result, nil
}

func runComponentMigrations(ctx context.Context, conn migrationConn, component string) error {
	for _, migration := range migrationCatalog[component] {
		if err := runMigrationFiles(ctx, conn, component, migration.Version, migration.Paths...); err != nil {
			return err
		}
	}
	return nil
}

func migrationPathsThrough(component string, version int) []string {
	var result []string
	for _, migration := range migrationCatalog[component] {
		if migration.Version > version {
			break
		}
		result = append(result, migration.Paths...)
	}
	return result
}
