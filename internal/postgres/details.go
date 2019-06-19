// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"
	"golang.org/x/discovery/internal"
	"golang.org/x/discovery/internal/derrors"
	"golang.org/x/discovery/internal/license"
)

// GetPackage returns the first package from the database that has path and
// version.
//
// The returned error may be checked with derrors.IsInvalidArgument to
// determine if it was caused by an invalid path or version.
func (db *DB) GetPackage(ctx context.Context, path string, version string) (*internal.VersionedPackage, error) {
	if path == "" || version == "" {
		return nil, derrors.InvalidArgument("path and version cannot be empty")
	}

	var (
		commitTime                                                                  time.Time
		name, synopsis, seriesPath, modulePath, suffix, readmeFilePath, versionType string
		readmeContents, documentation                                               []byte
		licenseTypes, licensePaths                                                  []string
	)
	query := `
		SELECT
			v.commit_time,
			p.license_types,
			p.license_paths,
			v.readme_file_path,
			v.readme_contents,
			m.series_path,
			v.module_path,
			p.name,
			p.synopsis,
			p.suffix,
			v.version_type,
			p.documentation
		FROM
			versions v
		INNER JOIN
			vw_licensed_packages p
		ON
			p.module_path = v.module_path
			AND v.version = p.version
		INNER JOIN
			modules m
		ON
		  m.path = v.module_path
		WHERE
			p.path = $1
			AND p.version = $2
		LIMIT 1;`

	row := db.QueryRowContext(ctx, query, path, version)
	if err := row.Scan(&commitTime, pq.Array(&licenseTypes),
		pq.Array(&licensePaths), &readmeFilePath, &readmeContents, &seriesPath, &modulePath,
		&name, &synopsis, &suffix, &versionType, &documentation); err != nil {
		if err == sql.ErrNoRows {
			return nil, derrors.NotFound(fmt.Sprintf("package %s@%s not found", path, version))
		}
		return nil, fmt.Errorf("row.Scan(): %v", err)
	}

	lics, err := zipLicenseMetadata(licenseTypes, licensePaths)
	if err != nil {
		return nil, fmt.Errorf("zipLicenseMetadata(%v, %v): %v", licenseTypes, licensePaths, err)
	}

	return &internal.VersionedPackage{
		Package: internal.Package{
			Name:              name,
			Path:              path,
			Synopsis:          synopsis,
			Licenses:          lics,
			Suffix:            suffix,
			DocumentationHTML: documentation,
		},
		VersionInfo: internal.VersionInfo{
			ModulePath:     modulePath,
			Version:        version,
			CommitTime:     commitTime,
			ReadmeFilePath: readmeFilePath,
			ReadmeContents: readmeContents,
			VersionType:    internal.VersionType(versionType),
		},
	}, nil
}

// GetLatestPackage returns the package from the database with the latest version.
// If multiple packages share the same path then the package that the database
// chooses is returned.
func (db *DB) GetLatestPackage(ctx context.Context, path string) (*internal.VersionedPackage, error) {
	if path == "" {
		return nil, fmt.Errorf("path cannot be empty")
	}

	var (
		commitTime                                                              time.Time
		seriesPath, modulePath, name, synopsis, version, suffix, readmeFilePath string
		licenseTypes, licensePaths                                              []string
		readmeContents, documentation                                           []byte
	)
	query := `
		SELECT
			m.series_path,
			p.module_path,
			p.license_types,
			p.license_paths,
			v.version,
			v.commit_time,
			p.name,
			p.synopsis,
			p.suffix,
			v.readme_file_path,
			v.readme_contents,
			p.documentation
		FROM
			versions v
		INNER JOIN
			modules m
		ON
			v.module_path = m.path
		INNER JOIN
			vw_licensed_packages p
		ON
			p.module_path = v.module_path
			AND v.version = p.version
		WHERE
			p.path = $1
		ORDER BY
			v.module_path,
			v.major DESC,
			v.minor DESC,
			v.patch DESC,
			v.prerelease DESC
		LIMIT 1;`

	row := db.QueryRowContext(ctx, query, path)
	if err := row.Scan(&seriesPath, &modulePath, pq.Array(&licenseTypes), pq.Array(&licensePaths), &version, &commitTime, &name, &synopsis, &suffix, &readmeFilePath, &readmeContents, &documentation); err != nil {
		if err == sql.ErrNoRows {
			return nil, derrors.NotFound(fmt.Sprintf("package %s@%s not found", path, version))
		}
		return nil, fmt.Errorf("row.Scan(): %v", err)
	}

	lics, err := zipLicenseMetadata(licenseTypes, licensePaths)
	if err != nil {
		return nil, fmt.Errorf("zipLicenseMetadata(%v, %v): %v", licenseTypes, licensePaths, err)
	}

	return &internal.VersionedPackage{
		Package: internal.Package{
			Name:              name,
			Path:              path,
			Synopsis:          synopsis,
			Licenses:          lics,
			Suffix:            suffix,
			DocumentationHTML: documentation,
		},
		VersionInfo: internal.VersionInfo{
			ModulePath:     modulePath,
			Version:        version,
			CommitTime:     commitTime,
			ReadmeFilePath: readmeFilePath,
			ReadmeContents: readmeContents,
		},
	}, nil
}

// GetVersionForPackage returns the module version corresponding to path and
// version. *internal.Version will contain all packages for that version, in
// sorted order by package path.
func (db *DB) GetVersionForPackage(ctx context.Context, path, version string) (*internal.Version, error) {
	query := `SELECT
		p.path,
		m.series_path,
		p.module_path,
		p.name,
		p.synopsis,
		p.suffix,
		p.license_types,
		p.license_paths,
		v.readme_file_path,
		v.readme_contents,
		v.commit_time,
		v.version_type,
		p.documentation
	FROM
		vw_licensed_packages p
	INNER JOIN
		versions v
	ON
		v.module_path = p.module_path
		AND v.version = p.version
	INNER JOIN
		modules m
	ON
		m.path = v.module_path
	WHERE
		p.version = $1
		AND p.module_path IN (
			SELECT module_path
			FROM packages
			WHERE path=$2
		)
	ORDER BY path;`

	var (
		pkgPath, seriesPath, modulePath, pkgName      string
		synopsis, suffix, readmeFilePath, versionType string
		readmeContents, documentation                 []byte
		commitTime                                    time.Time
		licenseTypes, licensePaths                    []string
	)

	rows, err := db.QueryContext(ctx, query, version, path)
	if err != nil {
		return nil, fmt.Errorf("db.QueryContext(ctx, %s, %q): %v", query, path, err)
	}
	defer rows.Close()

	v := &internal.Version{}
	v.Version = version
	for rows.Next() {
		if err := rows.Scan(&pkgPath, &seriesPath, &modulePath, &pkgName, &synopsis, &suffix,
			pq.Array(&licenseTypes), pq.Array(&licensePaths), &readmeFilePath,
			&readmeContents, &commitTime, &versionType, &documentation); err != nil {
			return nil, fmt.Errorf("row.Scan(): %v", err)
		}
		lics, err := zipLicenseMetadata(licenseTypes, licensePaths)
		if err != nil {
			return nil, fmt.Errorf("zipLicenseMetadata(%v, %v): %v", licenseTypes, licensePaths, err)
		}
		v.ModulePath = modulePath
		v.ReadmeFilePath = readmeFilePath
		v.ReadmeContents = readmeContents
		v.CommitTime = commitTime
		v.VersionType = internal.VersionType(versionType)
		v.Packages = append(v.Packages, &internal.Package{
			Path:              pkgPath,
			Name:              pkgName,
			Synopsis:          synopsis,
			Licenses:          lics,
			Suffix:            suffix,
			DocumentationHTML: documentation,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows.Err(): %v", err)
	}

	return v, nil
}

// GetTaggedVersionsForPackageSeries returns a list of tagged versions sorted
// in descending order by major, minor and patch number and then lexicographically
// in descending order by prerelease. This list includes tagged versions of
// packages that are part of the same series and have the same package suffix.
func (db *DB) GetTaggedVersionsForPackageSeries(ctx context.Context, path string) ([]*internal.VersionInfo, error) {
	return getVersions(ctx, db, path, []internal.VersionType{internal.VersionTypeRelease, internal.VersionTypePrerelease})
}

// GetPseudoVersionsForPackageSeries returns the 10 most recent from a list of
// pseudo-versions sorted in descending order by major, minor and patch number
// and then lexicographically in descending order by prerelease. This list includes
// pseudo-versions of packages that are part of the same series and have the same
// package suffix.
func (db *DB) GetPseudoVersionsForPackageSeries(ctx context.Context, path string) ([]*internal.VersionInfo, error) {
	return getVersions(ctx, db, path, []internal.VersionType{internal.VersionTypePseudo})
}

// getVersions returns a list of versions sorted numerically
// in descending order by major, minor and patch number and then
// lexicographically in descending order by prerelease. The version types
// included in the list are specified by a list of VersionTypes. The results
// include the type of versions of packages that are part of the same series
// and have the same package suffix as the package specified by the path.
func getVersions(ctx context.Context, db *DB, path string, versionTypes []internal.VersionType) ([]*internal.VersionInfo, error) {
	var (
		commitTime                                time.Time
		seriesPath, modulePath, synopsis, version string
		versionHistory                            []*internal.VersionInfo
	)

	// This query is complicated by the need to match my.mod/foo as history for
	// my.mod/v2/foo -- we need to match all packages in the series with the same
	// suffix.
	baseQuery := `WITH package_series AS (
			SELECT
				m.series_path,
				p.path AS package_path,
				p.suffix AS package_suffix,
				p.module_path,
				v.version,
				v.commit_time,
				p.synopsis,
				v.major,
				v.minor,
				v.patch,
				v.prerelease,
				v.version_type
			FROM
				modules m
			INNER JOIN
				packages p
			ON
				p.module_path = m.path
			INNER JOIN
				versions v
			ON
				p.module_path = v.module_path
				AND p.version = v.version
		)
		SELECT
			series_path,
			module_path,
			version,
			commit_time,
			synopsis
		FROM
			package_series
		WHERE
			(series_path, package_suffix) IN (
				SELECT series_path, package_suffix
				FROM package_series WHERE package_path=$1
			)
			AND (%s)
		ORDER BY
			module_path DESC,
			major DESC,
			minor DESC,
			patch DESC,
			prerelease DESC %s`

	queryEnd := `;`
	if len(versionTypes) == 0 {
		return nil, fmt.Errorf("error: must specify at least one version type")
	} else if len(versionTypes) == 1 && versionTypes[0] == internal.VersionTypePseudo {
		queryEnd = `LIMIT 10;`
	}

	var (
		vtQuery []string
		params  = []interface{}{path}
	)
	for i, vt := range versionTypes {
		vtQuery = append(vtQuery, fmt.Sprintf(`version_type = $%d`, i+2))
		params = append(params, vt.String())
	}

	query := fmt.Sprintf(baseQuery, strings.Join(vtQuery, " OR "), queryEnd)

	rows, err := db.QueryContext(ctx, query, params...)
	if err != nil {
		return nil, fmt.Errorf("db.QueryContext(ctx, %q, %q): %v", query, path, err)
	}
	defer rows.Close()

	for rows.Next() {
		if err := rows.Scan(&seriesPath, &modulePath, &version, &commitTime, &synopsis); err != nil {
			return nil, fmt.Errorf("row.Scan(): %v", err)
		}

		versionHistory = append(versionHistory, &internal.VersionInfo{
			ModulePath: modulePath,
			Version:    version,
			CommitTime: commitTime,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows.Err(): %v", err)
	}

	return versionHistory, nil
}

// GetImports fetches and returns all of the imports for the package with path
// and version. If multiple packages have the same path and version, all of
// the imports will be returned.
// The returned error may be checked with derrors.IsInvalidArgument to
// determine if it resulted from an invalid package path or version.
func (db *DB) GetImports(ctx context.Context, path, version string) ([]*internal.Import, error) {
	if path == "" || version == "" {
		return nil, derrors.InvalidArgument("path and version cannot be empty")
	}

	var toPath, toName string
	query := `
		SELECT
			to_name,
			to_path
		FROM
			imports
		WHERE
			from_path = $1
			AND from_version = $2
		ORDER BY
			to_path,
			to_name;`

	rows, err := db.QueryContext(ctx, query, path, version)
	if err != nil {
		return nil, fmt.Errorf("db.QueryContext(ctx, %q, %q, %q): %v", query, path, version, err)
	}
	defer rows.Close()

	var imports []*internal.Import
	for rows.Next() {
		if err := rows.Scan(&toName, &toPath); err != nil {
			return nil, fmt.Errorf("row.Scan(): %v", err)
		}
		imports = append(imports, &internal.Import{
			Name: toName,
			Path: toPath,
		})
	}
	return imports, nil
}

// GetImportedBy fetches and returns all of the packages that import the
// package with path.
// The returned error may be checked with derrors.IsInvalidArgument to
// determine if it resulted from an invalid package path or version.
func (db *DB) GetImportedBy(ctx context.Context, path string) ([]string, error) {
	if path == "" {
		return nil, derrors.InvalidArgument("path cannot be empty")
	}

	var fromPath string
	query := `
		SELECT
			DISTINCT ON (from_path) from_path
		FROM
			imports
		WHERE
			to_path = $1
		ORDER BY
			from_path;`

	rows, err := db.QueryContext(ctx, query, path)
	if err != nil {
		return nil, fmt.Errorf("db.Query(%q, %q) returned error: %v", query, path, err)
	}
	defer rows.Close()

	var importedby []string
	for rows.Next() {
		if err := rows.Scan(&fromPath); err != nil {
			return nil, fmt.Errorf("row.Scan(): %v", err)
		}
		importedby = append(importedby, fromPath)
	}
	return importedby, nil
}

// GetLicenses returns all licenses associated with the given package path and
// version.
//
// The returned error may be checked with derrors.IsInvalidArgument to
// determine if it resulted from an invalid package path or version.
func (db *DB) GetLicenses(ctx context.Context, pkgPath string, version string) ([]*license.License, error) {
	if pkgPath == "" || version == "" {
		return nil, derrors.InvalidArgument("pkgPath and version cannot be empty")
	}
	query := `
		SELECT
			l.type,
			l.file_path,
			l.contents
		FROM
			licenses l
		INNER JOIN
			package_licenses pl
		ON
			pl.module_path = l.module_path
			AND pl.version = l.version
			AND pl.file_path = l.file_path
		INNER JOIN
			packages p
		ON
			p.module_path = pl.module_path
			AND p.version = pl.version
			AND p.path = pl.package_path
		WHERE
			p.path = $1
			AND p.version = $2
		ORDER BY l.file_path;`

	var (
		licenseType, licensePath string
		contents                 []byte
	)
	rows, err := db.QueryContext(ctx, query, pkgPath, version)
	if err != nil {
		return nil, fmt.Errorf("db.QueryContext(ctx, %q, %q): %v", query, pkgPath, err)
	}
	defer rows.Close()

	var licenses []*license.License
	for rows.Next() {
		if err := rows.Scan(&licenseType, &licensePath, &contents); err != nil {
			return nil, fmt.Errorf("row.Scan(): %v", err)
		}
		licenses = append(licenses, &license.License{
			Metadata: license.Metadata{
				Type:     licenseType,
				FilePath: licensePath,
			},
			Contents: contents,
		})
	}
	sort.Slice(licenses, func(i, j int) bool {
		return compareLicenses(licenses[i].Metadata, licenses[j].Metadata)
	})
	return licenses, nil
}

// zipLicenseMetadata constructs license.Metadata from the given license types
// and paths, by zipping and then sorting.
func zipLicenseMetadata(licenseTypes []string, licensePaths []string) ([]*license.Metadata, error) {
	if len(licenseTypes) != len(licensePaths) {
		return nil, fmt.Errorf("BUG: got %d license types and %d license paths", len(licenseTypes), len(licensePaths))
	}
	var metadata []*license.Metadata
	for i, t := range licenseTypes {
		metadata = append(metadata, &license.Metadata{Type: t, FilePath: licensePaths[i]})
	}
	sort.Slice(metadata, func(i, j int) bool {
		return compareLicenses(*metadata[i], *metadata[j])
	})
	return metadata, nil
}

// compareLicenses reports whether i < j according to our license sorting
// semantics.
func compareLicenses(i, j license.Metadata) bool {
	if len(strings.Split(i.FilePath, "/")) > len(strings.Split(j.FilePath, "/")) {
		return true
	}
	if i.FilePath < j.FilePath {
		return true
	}
	return i.Type < j.Type
}

// GetVersion fetches a Version from the database with the primary key
// (module_path, version).
func (db *DB) GetVersion(ctx context.Context, modulePath string, version string) (*internal.VersionInfo, error) {
	var (
		commitTime                              time.Time
		seriesPath, readmeFilePath, versionType string
		readmeContents                          []byte
	)

	query := `
		SELECT
			m.series_path,
			v.commit_time,
			v.readme_file_path,
			v.readme_contents,
			v.version_type
		FROM
			versions v
		INNER JOIN
			modules m
		ON
			m.path = v.module_path
		WHERE module_path = $1 and version = $2;`
	row := db.QueryRowContext(ctx, query, modulePath, version)
	if err := row.Scan(&seriesPath, &commitTime, &readmeFilePath, &readmeContents, &versionType); err != nil {
		return nil, fmt.Errorf("row.Scan(): %v", err)
	}
	return &internal.VersionInfo{
		ModulePath:     modulePath,
		Version:        version,
		CommitTime:     commitTime,
		ReadmeFilePath: readmeFilePath,
		ReadmeContents: readmeContents,
		VersionType:    internal.VersionType(versionType),
	}, nil
}
