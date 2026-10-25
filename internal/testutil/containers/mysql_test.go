//go:build integration

package containers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// defaultMariaDBConfig is DefaultMySQLConfig with the flavor switched to MariaDB.
func defaultMariaDBConfig() MySQLConfig {
	cfg := DefaultMySQLConfig()
	cfg.Flavor = FlavorMariaDB
	return cfg
}

// TestResolveMySQLImage pins the server selection order: MYSQL_TEST_IMAGE wins,
// then the config fields, then the defaults, and invalid input fails loudly
// instead of falling back to MySQL.
func TestResolveMySQLImage(t *testing.T) {
	tests := []struct {
		name       string
		cfg        MySQLConfig
		envImage   string
		onCI       bool
		wantImage  string
		wantFlavor MySQLFlavor
		wantErr    string
	}{
		{
			name:       "defaults to MySQL 8.0",
			wantImage:  "mysql:8.0",
			wantFlavor: FlavorMySQL,
		},
		{
			name:       "default config selects MySQL 8.0",
			cfg:        DefaultMySQLConfig(),
			wantImage:  "mysql:8.0",
			wantFlavor: FlavorMySQL,
		},
		{
			name:       "default config with MariaDB flavor uses the MariaDB default tag",
			cfg:        defaultMariaDBConfig(),
			wantImage:  "mariadb:11.8",
			wantFlavor: FlavorMariaDB,
		},
		{
			name:       "explicit tag wins over the flavor default",
			cfg:        MySQLConfig{Flavor: FlavorMariaDB, ImageTag: "10.11"},
			wantImage:  "mariadb:10.11",
			wantFlavor: FlavorMariaDB,
		},
		{
			name:       "env wins over a pinned config tag",
			cfg:        MySQLConfig{ImageTag: "8.0"},
			envImage:   "mariadb:11.8",
			wantImage:  "mariadb:11.8",
			wantFlavor: FlavorMariaDB,
		},
		{
			name:       "config flavor uses the flavor default tag",
			cfg:        MySQLConfig{Flavor: FlavorMariaDB},
			wantImage:  "mariadb:11.8",
			wantFlavor: FlavorMariaDB,
		},
		{
			name:     "env with an unknown repository is an error",
			envImage: "postgres:16",
			wantErr:  MySQLImageEnvVar,
		},
		{
			name:     "env without a tag is an error",
			envImage: "mariadb",
			wantErr:  MySQLImageEnvVar,
		},
		{
			name:       "env wins on CI",
			envImage:   "mariadb:10.11",
			onCI:       true,
			wantImage:  "mariadb:10.11",
			wantFlavor: FlavorMariaDB,
		},
		{
			name:     "env with an empty tag is an error",
			envImage: "mysql:",
			wantErr:  MySQLImageEnvVar,
		},
		{
			name:    "unknown config flavor is an error",
			cfg:     MySQLConfig{Flavor: "postgres"},
			wantErr: "unknown database flavor",
		},
		{
			name:    "unset env on CI is an error",
			onCI:    true,
			wantErr: "must be set on CI",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			image, flavor, err := resolveMySQLImage(&tt.cfg, tt.envImage, tt.onCI)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantImage, image)
			assert.Equal(t, tt.wantFlavor, flavor)
		})
	}
}
