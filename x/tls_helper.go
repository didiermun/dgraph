/*
 * Copyright 2017-2018 Dgraph Labs, Inc.
 *
 * This file is available under the Apache License, Version 2.0,
 * with the Commons Clause restriction.
 */

package x

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
    "encoding/json" 
	"fmt"
	"io/ioutil"
	"strings"
	"sync"
	"time"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

type tlsConfigType int8

const (
	TLSClientConfig tlsConfigType = iota
	TLSServerConfig
)

// TLSHelperConfig define params used to create a tls.Config
type TLSHelperConfig struct {
	ConfigType             tlsConfigType
	CertRequired           bool
	Cert                   string
	Key                    string
	KeyPassphrase          string
	ServerName             string
	Insecure               bool
	RootCACerts            string
	UseSystemRootCACerts   bool
	ClientAuth             string
	ClientCACerts          string
	UseSystemClientCACerts bool
	MinVersion             string
	MaxVersion             string
}

func RegisterTLSFlags(flag *pflag.FlagSet) {
	// TODO: Why is the naming of the flags inconsistent here?
	flag.Bool("tls_on", false, "Use TLS connections with clients.")
	flag.String("tls_cert", "", "Certificate file path.")
	flag.String("tls_cert_key", "", "Certificate key file path.")
	flag.String("tls_cert_key_passphrase", "", "Certificate key passphrase.")
	flag.Bool("tls_use_system_ca", false, "Include System CA into CA Certs.")
	flag.String("tls_min_version", "TLS11", "TLS min version.")
	flag.String("tls_max_version", "TLS12", "TLS max version.")
}

func LoadTLSConfig(conf *TLSHelperConfig, v *viper.Viper) {
	conf.CertRequired = v.GetBool("tls_on")
	conf.Cert = v.GetString("tls_cert")
	conf.Key = v.GetString("tls_cert_key")
	conf.KeyPassphrase = v.GetString("tls_cert_key_passphrase")
	conf.UseSystemClientCACerts = v.GetBool("tls_use_system_ca")
	conf.MinVersion = v.GetString("tls_min_version")
	conf.MaxVersion = v.GetString("tls_max_version")
}

func generateCertPool(certPath string, useSystemCA bool) (*x509.CertPool, error) {
	var pool *x509.CertPool
	if useSystemCA {
		var err error
		if pool, err = x509.SystemCertPool(); err != nil {
			return nil, err
		}
	} else {
		pool = x509.NewCertPool()
	}

	if len(certPath) > 0 {
		caFile, err := ioutil.ReadFile(certPath)
		if err != nil {
			return nil, err
		}
		if !pool.AppendCertsFromPEM(caFile) {
			return nil, fmt.Errorf("Error reading CA file '%s'.\n%s", certPath, err)
		}
	}

	return pool, nil
}

func parseCertificate(cert []byte, certKey []byte, certKeyPass string) (*tls.Certificate, error) {
    if block, _ := pem.Decode(certKey); block != nil {
        if true {
            decryptKey, err := x509.DecryptPEMBlock(block, []byte(certKeyPass))
            if err != nil {
                return nil, err
            }
            
            privKey, err := x509.ParsePKCS1PrivateKey(decryptKey)
            if err != nil {
                return nil, err
            }
            
            certKey = pem.EncodeToMemory(&pem.Block{
                Type:  "RSA PRIVATE KEY",
                Bytes: x509.MarshalPKCS1PrivateKey(privKey),
            })
        } else {
            certKey = pem.EncodeToMemory(block)
        }
    } else {
        return nil, fmt.Errorf("Invalid Cert Key")
    }
    
    // Load certificate, pair cert/key
    certificate, err := tls.X509KeyPair(cert, certKey)
    if err != nil {
        return nil, fmt.Errorf("Error installing certificates", err)
    }
    
    return &certificate, nil
}

func setupVersion(cfg *tls.Config, minVersion string, maxVersion string) error {
	// Configure TLS version
	tlsVersion := map[string]uint16{
		"TLS11": tls.VersionTLS11,
		"TLS12": tls.VersionTLS12,
	}

	if len(minVersion) > 0 {
		if val, has := tlsVersion[strings.ToUpper(minVersion)]; has {
			cfg.MinVersion = val
		} else {
			return fmt.Errorf("Invalid min_version '%s'. Valid values [TLS11, TLS12]", minVersion)
		}
	} else {
		cfg.MinVersion = tls.VersionTLS11
	}

	if len(maxVersion) > 0 {
		if val, has := tlsVersion[strings.ToUpper(maxVersion)]; has && val >= cfg.MinVersion {
			cfg.MaxVersion = val
		} else {
			if has {
				return fmt.Errorf("Cannot use '%s' as max_version, it's lower than '%s'", maxVersion, minVersion)
			}
			return fmt.Errorf("Invalid max_version '%s'. Valid values [TLS11, TLS12]", maxVersion)
		}
	} else {
		cfg.MaxVersion = tls.VersionTLS12
	}
	return nil
}

func setupClientAuth(authType string) (tls.ClientAuthType, error) {
	auth := map[string]tls.ClientAuthType{
		"REQUEST":          tls.RequestClientCert,
		"REQUIREANY":       tls.RequireAnyClientCert,
		"VERIFYIFGIVEN":    tls.VerifyClientCertIfGiven,
		"REQUIREANDVERIFY": tls.RequireAndVerifyClientCert,
	}

	if len(authType) > 0 {
		if v, has := auth[strings.ToUpper(authType)]; has {
			return v, nil
		}
		return tls.NoClientCert, fmt.Errorf("Invalid client auth. Valid values [REQUEST, REQUIREANY, VERIFYIFGIVEN, REQUIREANDVERIFY]")
	}

	return tls.NoClientCert, nil
}

// GenerateTLSConfig creates and returns a new *tls.Config with the
// configuration provided. If the ConfigType provided in TLSHelperConfig is
// TLSServerConfig, it's return a reload function. If any problem is found, an
// error is returned

func GenerateTLSConfigServer(config TLSHelperConfig) (tlsCfg *tls.Config, reloadConfig func([]byte), err error) {
    wrapper := new(wrapperTLSConfig)
	tlsCfg = new(tls.Config)
	wrapper.config = tlsCfg
    wrapper.cert = &wrapperCert{}
    tlsCfg.GetCertificate = wrapper.getCertificate
    tlsCfg.VerifyPeerCertificate = wrapper.verifyPeerCertificate
    wrapper.helperConfig = &config
    return wrapper.config, wrapper.reloadConfigJson, nil
}

// different one for server and client
func GenerateTLSConfig(config TLSHelperConfig) (tlsCfg *tls.Config, reloadConfig func([]byte), err error) {
	wrapper := new(wrapperTLSConfig)
	tlsCfg = new(tls.Config)
	wrapper.config = tlsCfg
    wrapper.reloadConfig()

	auth, err := setupClientAuth(config.ClientAuth)
	if err != nil {
		return nil, nil, err
	}

	// If the client cert is required to be checked with the CAs
	if auth >= tls.VerifyClientCertIfGiven {
		// A custom cert validation is set because the current implementation is
		// not thread safe, it's needed bypass that validation and manually
		// manage the different cases, for that reason, the wrapper is
		// configured with the real auth level and the tlsCfg is only set with a
		// auth level who are a simile but without the use of any CA
		if auth == tls.VerifyClientCertIfGiven {
			tlsCfg.ClientAuth = tls.RequestClientCert
		} else {
			tlsCfg.ClientAuth = tls.RequireAnyClientCert
		}
		wrapper.clientAuth = auth
	} else {
		// it's not necessary a external validation with the CAs, so the wrapper
		// is not used
		tlsCfg.ClientAuth = auth
	}

	// Configure Root CAs
    // xxx - should never use the system certs
	if len(config.RootCACerts) > 0 || config.UseSystemRootCACerts {
		pool, err := generateCertPool(config.RootCACerts, config.UseSystemRootCACerts)
		if err != nil {
			return nil, nil, err
		}
		tlsCfg.RootCAs = pool
	}

	// Configure Client CAs
	if len(config.ClientCACerts) > 0 || config.UseSystemClientCACerts {
		pool, err := generateCertPool(config.ClientCACerts, config.UseSystemClientCACerts)
		if err != nil {
			return nil, nil, err
		}
		tlsCfg.ClientCAs = x509.NewCertPool()
		wrapper.clientCAPool = &wrapperCAPool{pool: pool}
	}

	err = setupVersion(tlsCfg, config.MinVersion, config.MaxVersion)
	if err != nil {
		return nil, nil, err
	}

	tlsCfg.InsecureSkipVerify = config.Insecure
	tlsCfg.ServerName = config.ServerName

	if config.ConfigType == TLSClientConfig {
		return tlsCfg, nil, nil
	}

	wrapper.helperConfig = &config
	return tlsCfg, wrapper.reloadConfigJson, nil
}

type wrapperCert struct {
	sync.RWMutex
	cert *tls.Certificate
}

type wrapperCAPool struct {
	sync.RWMutex
	pool *x509.CertPool
}

type wrapperTLSConfig struct {
	mutex        sync.Mutex
	cert         *wrapperCert
	clientCert   *wrapperCert
	clientCAPool *wrapperCAPool
	clientAuth   tls.ClientAuthType
	config       *tls.Config
	helperConfig *TLSHelperConfig
}

func (c *wrapperTLSConfig) getCertificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	c.cert.RLock()
	cert := c.cert.cert
	c.cert.RUnlock()
	return cert, nil
}

func (c *wrapperTLSConfig) getClientCertificate(_ *tls.CertificateRequestInfo) (*tls.Certificate, error) {
	c.clientCert.RLock()
	cert := c.clientCert.cert
	c.clientCert.RUnlock()
	return cert, nil
}

func (c *wrapperTLSConfig) verifyPeerCertificate(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
	if c.clientAuth >= tls.VerifyClientCertIfGiven && len(rawCerts) > 0 {
		if len(rawCerts) > 0 {
			pool := x509.NewCertPool()
			for _, raw := range rawCerts[1:] {
				if cert, err := x509.ParseCertificate(raw); err == nil {
					pool.AddCert(cert)
				} else {
					return Errorf("Invalid certificate")
				}
			}

			c.clientCAPool.RLock()
			clientCAs := c.clientCAPool.pool
			c.clientCAPool.RUnlock()
			opts := x509.VerifyOptions{
				Intermediates: pool,
				Roots:         clientCAs,
				CurrentTime:   time.Now(),
				KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
			}

			cert, err := x509.ParseCertificate(rawCerts[0])
			if err != nil {
				return err
			}
			_, err = cert.Verify(opts)
			if err != nil {
				return Errorf("Failed to verify certificate")
			}
		} else {
			return Errorf("Invalid certificate")
		}
	}
	return nil
}

// move me please, need to be visible by the populator
type TlsInfo struct {
    Cert string `json:"cert"` 
    CertKey string `json:"certKey"`
    CertKeyPassPhrase string `json:"certKeyPassPhrase"`
}

func (c *wrapperTLSConfig) reloadConfigJson(jsonKeys []byte) {
	c.mutex.Lock()
	defer c.mutex.Unlock()    
    ti := &TlsInfo{}
    err := json.Unmarshal(jsonKeys, ti)
    
    if err != nil {
        fmt.Println("json key unmarshal error", string(jsonKeys), ti, c.helperConfig)
    } 

    if c.helperConfig.CertRequired {    
        cert, err := parseCertificate( []byte(ti.Cert), []byte(ti.CertKey), ti.CertKeyPassPhrase)
        if err != nil {
            Printf("Error reloading certificate. %s\nUsing current certificate\n", err.Error())
        } else if cert != nil {
			c.cert.Lock()
			c.cert.cert = cert
			c.cert.Unlock()
		}
    }
}

func (c *wrapperTLSConfig) reloadConfig() {
	c.mutex.Lock()
	defer c.mutex.Unlock()

    if c.helperConfig.CertRequired {
        // Load cert
        certText, err := ioutil.ReadFile(c.helperConfig.Cert)
        if err != nil {
            // log
            fmt.Println("couldn't load cert file")
        }

        // Load private key associated with cert
        key, err := ioutil.ReadFile(c.helperConfig.Key)
        if err != nil {
            fmt.Println("couldn't load key file")
        }
        
        // Loading new certificate
        cert, err := parseCertificate(certText, key, c.helperConfig.KeyPassphrase)
        if err != nil {
            Printf("Error reloading certificate. %s\nUsing current certificate\n", err.Error())
        } else if cert != nil {
            if c.helperConfig.ConfigType == TLSServerConfig {
                c.cert.Lock()
                c.cert.cert = cert
                c.cert.Unlock()
            }
        }
        if c.helperConfig.ConfigType == TLSClientConfig {
            c.config.Certificates = []tls.Certificate{*cert}
			c.config.BuildNameToCertificate()
        }
    }
    
    // Configure Client CAs - is this server or client?
    if len(c.helperConfig.ClientCACerts) > 0 || c.helperConfig.UseSystemClientCACerts {
        pool, err := generateCertPool(c.helperConfig.ClientCACerts, c.helperConfig.UseSystemClientCACerts)
        if err != nil {
            Printf("Error reloading CAs. %s\nUsing current Client CAs\n", err.Error())
        } else {
			c.clientCAPool.Lock()
			c.clientCAPool.pool = pool
			c.clientCAPool.Unlock()
		}
	}
}
