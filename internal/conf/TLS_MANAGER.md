# TLS Certificate Manager

## Overview

The TLS Certificate Manager provides a secure, centralized system for managing TLS certificates and private keys across different services in BirdNET-Go. It handles certificate validation, secure storage, and file path management, making it easy to add TLS support to any service.

## Purpose

This manager addresses several key requirements:
- **Security**: Store certificates and keys with proper file permissions
- **Convenience**: Allow users to paste certificates directly in the UI
- **Scalability**: Support multiple services (MQTT, MySQL, etc.) with isolated storage
- **Maintainability**: Centralize certificate handling logic

## Architecture

### File Structure
```
config/
└── tls/                    # Root TLS directory (0700)
    ├── mqtt/              # Service-specific directory (0700)
    │   ├── mqtt_ca.crt    # CA certificate (0644)
    │   ├── mqtt_client.crt # Client certificate (0644)
    │   └── mqtt_client.key # Client private key (0600)
    ├── mysql/             # Future service example
    │   └── ...
    └── redis/             # Another future service
        └── ...
```

### Key Components

1. **TLSManager** (`tls.go`):
   - Core certificate management functionality
   - Service-based directory isolation
   - File permission enforcement
   - Certificate validation

2. **Certificate Types**:
   - `TLSCertTypeCA`: Certificate Authority certificates
   - `TLSCertTypeClient`: Client certificates
   - `TLSCertTypeKey`: Private keys

## API Reference

### Initialization

```go
// Get the global TLS manager instance
tlsManager := conf.GetTLSManager()

// Or create a custom instance
tlsManager := conf.NewTLSManager("/path/to/config")
```

### Core Methods

#### SaveCertificate
```go
func (tm *TLSManager) SaveCertificate(service string, certType TLSCertificateType, content string) (string, error)
```
Saves a certificate or key with proper validation and permissions.
- Returns the file path on success
- Empty content removes the certificate file
- Validates PEM format and certificate structure

#### GetCertificatePath
```go
func (tm *TLSManager) GetCertificatePath(service string, certType TLSCertificateType) string
```
Returns the path where a certificate would be stored.

#### CertificateExists
```go
func (tm *TLSManager) CertificateExists(service string, certType TLSCertificateType) bool
```
Checks if a certificate file exists for the service.

#### RemoveCertificate
```go
func (tm *TLSManager) RemoveCertificate(service string, certType TLSCertificateType) error
```
Removes a specific certificate file.

#### RemoveAllCertificates
```go
func (tm *TLSManager) RemoveAllCertificates(service string) error
```
Removes all certificates for a service.

## Usage Examples

### Adding TLS Support to a New Service

1. **Update Configuration Structure**:
```go
type MySQLTLSSettings struct {
    Enabled            bool   `yaml:"enabled"`
    InsecureSkipVerify bool   `yaml:"insecureSkipVerify"`
    CACert             string `yaml:"cacert,omitempty"`     // File path
    ClientCert         string `yaml:"clientcert,omitempty"` // File path
    ClientKey          string `yaml:"clientkey,omitempty"`  // File path
}
```

2. **Process Certificates in Settings Handler**:
```go
// In processTLSCertificates function, add:
"realtime.mysql.tls.cacert": {
    service:  "mysql",
    certType: conf.TLSCertTypeCA,
    pathPtr:  &settings.Realtime.MySQL.TLS.CACert,
},
// ... add client cert and key entries
```

3. **Use in Your Service Client**:
```go
// Read certificates from the managed paths
if config.TLS.CACert != "" {
    caCert, err := os.ReadFile(config.TLS.CACert)
    if err != nil {
        return fmt.Errorf("failed to read CA cert: %w", err)
    }
    // Use caCert in your TLS configuration
}
```

### UI Integration

1. **Add Certificate Fields**:
```html
<textarea 
    id="mysqlTlsCaCert"
    x-model="mysql.tls.caCert"
    name="realtime.mysql.tls.cacert"
    class="textarea textarea-bordered textarea-sm h-24"
    placeholder="-----BEGIN CERTIFICATE-----&#10;...&#10;-----END CERTIFICATE-----">
</textarea>
```

2. **Backend Processing**:
The settings handler will automatically:
- Extract certificate content from form
- Validate and save to secure location
- Update configuration with file path
- Remove form field to prevent double processing

## Security Considerations

### File Permissions
- **Directories**: 0700 (owner read/write/execute only)
- **Private Keys**: 0600 (owner read/write only)
- **Certificates**: 0644 (owner write, all read)

### Certificate Validation
- Validates PEM encoding structure
- Checks certificate parsing for CA and client certificates
- Validates private key block types (RSA, EC, PKCS#8)

### Best Practices
1. **Never store certificates in config files**: Use file paths only
2. **Validate user input**: All certificates are validated before storage
3. **Isolate by service**: Each service has its own directory
4. **Clean up on empty input**: Empty content removes the file
5. **Use descriptive filenames**: `service_type.ext` format

## Error Handling

All errors include:
- **Component**: "tls-manager"
- **Category**: Appropriate error category
- **Context**: Operation, file paths, service name

Example:
```go
errors.New(err).
    Component("tls-manager").
    Category(errors.CategoryFileIO).
    Context("operation", "write-cert").
    Context("file", filePath).
    Build()
```

## Testing

### Unit Testing
```go
// Test certificate validation
func TestValidateCertificateContent(t *testing.T) {
    // Test valid certificate
    validCert := "-----BEGIN CERTIFICATE-----\n..."
    err := validateCertificateContent(TLSCertTypeCA, validCert)
    assert.NoError(t, err)
    
    // Test invalid PEM
    err = validateCertificateContent(TLSCertTypeCA, "invalid")
    assert.Error(t, err)
}
```

### Integration Testing
```go
// Test full save/load cycle
func TestCertificateLifecycle(t *testing.T) {
    tm := NewTLSManager(t.TempDir())
    
    // Save certificate
    path, err := tm.SaveCertificate("test", TLSCertTypeCA, testCert)
    assert.NoError(t, err)
    assert.FileExists(t, path)
    
    // Verify permissions
    info, _ := os.Stat(path)
    assert.Equal(t, os.FileMode(0644), info.Mode())
}
```

## Future Enhancements

Potential improvements:
1. **Certificate expiration monitoring**
2. **Automatic certificate renewal support**
3. **Certificate chain validation**
4. **Key rotation automation**
5. **Backup and restore functionality**
6. **Certificate metadata storage** (expiry, issuer, etc.)
7. **Multi-tenant support** with user isolation
8. **Audit logging** for certificate operations

## Migration Guide

For existing installations:
1. Certificates previously stored as content in config will continue to work
2. New saves through UI will migrate to file-based storage
3. Manual migration: paste existing certificates into UI and save

## Troubleshooting

### Common Issues

1. **Permission Denied**:
   - Check directory ownership
   - Verify process user has write access to config directory

2. **Invalid Certificate Error**:
   - Ensure PEM format with proper headers/footers
   - Check for extra whitespace or characters
   - Validate certificate hasn't expired

3. **Path Not Found**:
   - TLS directory is created automatically
   - Check config directory path is correct

### Debug Tips
- Check file permissions: `ls -la config/tls/service/`
- Validate certificate: `openssl x509 -in cert.pem -text -noout`
- Test private key: `openssl rsa -in key.pem -check`