package main

import (
	"crypto"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"time"

	"github.com/letsencrypt/boulder/core"
	"github.com/letsencrypt/boulder/pkcs11helpers"
	"golang.org/x/crypto/ocsp"
	"gopkg.in/yaml.v2"
)

const configDateLayout = "2006-01-02 15:04:05"

type keyGenConfig struct {
	Type         string `yaml:"type"`
	RSAModLength uint   `yaml:"rsa-mod-length"`
	ECDSACurve   string `yaml:"ecdsa-curve"`
}

var allowedCurves = map[string]bool{
	"P-224": true,
	"P-256": true,
	"P-384": true,
	"P-521": true,
}

func (kgc keyGenConfig) validate() error {
	if kgc.Type == "" {
		return errors.New("key.type is required")
	}
	if kgc.Type != "rsa" && kgc.Type != "ecdsa" {
		return errors.New("key.type can only be 'rsa' or 'ecdsa'")
	}
	if kgc.Type == "rsa" && (kgc.RSAModLength != 2048 && kgc.RSAModLength != 4096) {
		return errors.New("key.rsa-mod-length can only be 2048 or 4096")
	}
	if kgc.Type == "rsa" && kgc.ECDSACurve != "" {
		return errors.New("if key.type = 'rsa' then key.ecdsa-curve is not used")
	}
	if kgc.Type == "ecdsa" && !allowedCurves[kgc.ECDSACurve] {
		return errors.New("key.ecdsa-curve can only be 'P-224', 'P-256', 'P-384', or 'P-521'")
	}
	if kgc.Type == "ecdsa" && kgc.RSAModLength != 0 {
		return errors.New("if key.type = 'ecdsa' then key.rsa-mod-length is not used")
	}

	return nil
}

type rootConfig struct {
	CeremonyType string `yaml:"ceremony-type"`
	PKCS11       struct {
		Module     string `yaml:"module"`
		PIN        string `yaml:"pin"`
		StoreSlot  uint   `yaml:"store-key-in-slot"`
		StoreLabel string `yaml:"store-key-with-label"`
	} `yaml:"pkcs11"`
	Key     keyGenConfig `yaml:"key"`
	Outputs struct {
		PublicKeyPath   string `yaml:"public-key-path"`
		CertificatePath string `yaml:"certificate-path"`
	} `yaml:"outputs"`
	CertProfile certProfile `yaml:"certificate-profile"`
}

func (rc rootConfig) validate() error {
	// PKCS11 fields
	if rc.PKCS11.Module == "" {
		return errors.New("pkcs11.module is required")
	}
	// key-slot cannot be tested because 0 is a valid slot
	if rc.PKCS11.StoreLabel == "" {
		return errors.New("pkcs11.store-key-with-label is required")
	}

	// Key gen fields
	if err := rc.Key.validate(); err != nil {
		return err
	}

	// Output fields
	if rc.Outputs.PublicKeyPath == "" {
		return errors.New("outputs.public-key-path is required")
	}
	if rc.Outputs.CertificatePath == "" {
		return errors.New("outputs.certificate-path is required")
	}

	// Certificate profile
	if err := rc.CertProfile.verifyProfile(rootCert); err != nil {
		return err
	}

	return nil
}

type intermediateConfig struct {
	CeremonyType string `yaml:"ceremony-type"`
	PKCS11       struct {
		Module       string `yaml:"module"`
		PIN          string `yaml:"pin"`
		SigningSlot  uint   `yaml:"signing-key-slot"`
		SigningLabel string `yaml:"signing-key-label"`
		SigningKeyID string `yaml:"signing-key-id"`
	} `yaml:"pkcs11"`
	Inputs struct {
		PublicKeyPath         string `yaml:"public-key-path"`
		IssuerCertificatePath string `yaml:"issuer-certificate-path"`
	} `yaml:"inputs"`
	Outputs struct {
		CertificatePath string `yaml:"certificate-path"`
	} `yaml:"outputs"`
	CertProfile certProfile `yaml:"certificate-profile"`
}

func (ic intermediateConfig) validate(ct certType) error {
	// PKCS11 fields
	if ic.PKCS11.Module == "" {
		return errors.New("pkcs11.module is required")
	}
	// key-slot cannot be tested because 0 is a valid slot
	if ic.PKCS11.SigningLabel == "" {
		return errors.New("pkcs11.signing-key-label is required")
	}
	if ic.PKCS11.SigningKeyID == "" {
		return errors.New("pkcs11.signing-key-id is required")
	}

	// Input fields
	if ic.Inputs.PublicKeyPath == "" {
		return errors.New("inputs.public-key-path is required")
	}
	if ic.Inputs.IssuerCertificatePath == "" {
		return errors.New("inputs.issuer-certificate is required")
	}

	// Output fields
	if ic.Outputs.CertificatePath == "" {
		return errors.New("outputs.certificate-path is required")
	}

	// Certificate profile
	if err := ic.CertProfile.verifyProfile(ct); err != nil {
		return err
	}

	return nil
}

type keyConfig struct {
	CeremonyType string `yaml:"ceremony-type"`
	PKCS11       struct {
		Module     string `yaml:"module"`
		PIN        string `yaml:"pin"`
		StoreSlot  uint   `yaml:"store-key-in-slot"`
		StoreLabel string `yaml:"store-key-with-label"`
	} `yaml:"pkcs11"`
	Key     keyGenConfig `yaml:"key"`
	Outputs struct {
		PublicKeyPath string `yaml:"public-key-path"`
	} `yaml:"outputs"`
}

func (kc keyConfig) validate() error {
	// PKCS11 fields
	if kc.PKCS11.Module == "" {
		return errors.New("pkcs11.module is required")
	}
	// key-slot cannot be tested because 0 is a valid slot
	if kc.PKCS11.StoreLabel == "" {
		return errors.New("pkcs11.store-key-with-label is required")
	}

	// Key gen fields
	if err := kc.Key.validate(); err != nil {
		return err
	}

	// Output fields
	if kc.Outputs.PublicKeyPath == "" {
		return errors.New("outputs.public-key-path is required")
	}

	return nil
}

type ocspRespConfig struct {
	CeremonyType string `yaml:"ceremony-type"`
	PKCS11       struct {
		Module       string `yaml:"module"`
		PIN          string `yaml:"pin"`
		SigningSlot  uint   `yaml:"signing-key-slot"`
		SigningLabel string `yaml:"signing-key-label"`
		SigningKeyID string `yaml:"signing-key-id"`
	} `yaml:"pkcs11"`
	Inputs struct {
		CertificatePath                string `yaml:"certificate-path"`
		IssuerCertificatePath          string `yaml:"issuer-certificate-path"`
		DelegatedIssuerCertificatePath string `yaml:"delegated-issuer-certificate-path"`
	} `yaml:"inputs"`
	Outputs struct {
		ResponsePath string `yaml:"response-path"`
	} `yaml:"outputs"`
	OCSPProfile struct {
		ThisUpdate string `yaml:"this-update"`
		NextUpdate string `yaml:"next-update"`
		Status     string `yaml:"status"`
	} `yaml:"ocsp-profile"`
}

func (orc ocspRespConfig) validate() error {
	// PKCS11 fields
	if orc.PKCS11.Module == "" {
		return errors.New("pkcs11.module is required")
	}
	// key-slot cannot be tested because 0 is a valid slot
	if orc.PKCS11.SigningLabel == "" {
		return errors.New("pkcs11.signing-key-label is required")
	}
	if orc.PKCS11.SigningKeyID == "" {
		return errors.New("pkcs11.signing-key-id is required")
	}

	// Input fields
	if orc.Inputs.CertificatePath == "" {
		return errors.New("inputs.certificate-path is required")
	}
	if orc.Inputs.IssuerCertificatePath == "" {
		return errors.New("inputs.issuer-certificate-path is required")
	}
	// DelegatedIssuerCertificatePath may be omitted

	// Output fields
	if orc.Outputs.ResponsePath == "" {
		return errors.New("outputs.response-path is required")
	}

	// OCSP fields
	if orc.OCSPProfile.ThisUpdate == "" {
		return errors.New("ocsp-profile.this-update is required")
	}
	if orc.OCSPProfile.NextUpdate == "" {
		return errors.New("ocsp-profile.next-update is required")
	}
	if orc.OCSPProfile.Status != "good" && orc.OCSPProfile.Status != "revoked" {
		return errors.New("ocsp-profile.status must be either \"good\" or \"revoked\"")
	}

	return nil
}

func signAndWriteCert(tbs, issuer *x509.Certificate, subjectPubKey crypto.PublicKey, signer crypto.Signer, certPath string) error {
	// x509.CreateCertificate uses a io.Reader here for signing methods that require
	// a source of randomness. Since PKCS#11 based signing generates needed randomness
	// at the HSM we don't need to pass a real reader. Instead of passing a nil reader
	// we use one that always returns errors in case the internal usage of this reader
	// changes.
	certBytes, err := x509.CreateCertificate(&failReader{}, tbs, issuer, subjectPubKey, signer)
	if err != nil {
		return fmt.Errorf("failed to create certificate: %s", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certBytes})
	log.Printf("Signed certificate PEM:\n%s", pemBytes)
	cert, err := x509.ParseCertificate(certBytes)
	if err != nil {
		return fmt.Errorf("failed to parse signed certificate: %s", err)
	}
	if tbs == issuer {
		// If cert is self-signed we need to populate the issuer subject key to
		// verify the signature
		issuer.PublicKey = cert.PublicKey
		issuer.PublicKeyAlgorithm = cert.PublicKeyAlgorithm
	}

	if err := cert.CheckSignatureFrom(issuer); err != nil {
		return fmt.Errorf("failed to verify certificate signature: %s", err)
	}
	log.Printf("Certificate PEM:\n%s", pemBytes)
	if err := ioutil.WriteFile(certPath, pemBytes, 0644); err != nil {
		return fmt.Errorf("failed to write certificate to %q: %s", certPath, err)
	}
	log.Printf("Certificate written to %q\n", certPath)
	return nil
}

func rootCeremony(configBytes []byte) error {
	var config rootConfig
	err := yaml.UnmarshalStrict(configBytes, &config)
	if err != nil {
		return fmt.Errorf("failed to parse config: %s", err)
	}
	if err := config.validate(); err != nil {
		return fmt.Errorf("failed to validate config: %s", err)
	}
	ctx, session, err := pkcs11helpers.Initialize(config.PKCS11.Module, config.PKCS11.StoreSlot, config.PKCS11.PIN)
	if err != nil {
		return fmt.Errorf("failed to setup session and PKCS#11 context for slot %d: %s", config.PKCS11.StoreSlot, err)
	}
	log.Printf("Opened PKCS#11 session for slot %d\n", config.PKCS11.StoreSlot)
	keyInfo, err := generateKey(ctx, session, config.PKCS11.StoreLabel, config.Outputs.PublicKeyPath, config.Key)
	if err != nil {
		return err
	}
	signer, err := newSigner(ctx, session, config.PKCS11.StoreLabel, keyInfo.id)
	if err != nil {
		return fmt.Errorf("failed to retrieve signer: %s", err)
	}
	template, err := makeTemplate(newRandReader(ctx, session), &config.CertProfile, keyInfo.der, rootCert)
	if err != nil {
		return fmt.Errorf("failed to create certificate profile: %s", err)
	}

	err = signAndWriteCert(template, template, keyInfo.key, signer, config.Outputs.CertificatePath)
	if err != nil {
		return err
	}

	return nil
}

func intermediateCeremony(configBytes []byte, ct certType) error {
	var config intermediateConfig
	err := yaml.UnmarshalStrict(configBytes, &config)
	if err != nil {
		return fmt.Errorf("failed to parse config: %s", err)
	}
	if err := config.validate(ct); err != nil {
		return fmt.Errorf("failed to validate config: %s", err)
	}

	ctx, session, err := pkcs11helpers.Initialize(config.PKCS11.Module, config.PKCS11.SigningSlot, config.PKCS11.PIN)
	if err != nil {
		return fmt.Errorf("failed to setup session and PKCS#11 context for slot %d: %s", config.PKCS11.SigningSlot, err)
	}
	log.Printf("Opened PKCS#11 session for slot %d\n", config.PKCS11.SigningSlot)
	keyID, err := hex.DecodeString(config.PKCS11.SigningKeyID)
	if err != nil {
		return fmt.Errorf("failed to decode key-id: %s", err)
	}
	signer, err := newSigner(ctx, session, config.PKCS11.SigningLabel, keyID)
	if err != nil {
		return fmt.Errorf("failed to retrieve private key handle: %s", err)
	}
	log.Println("Retrieved private key handle")

	pubPEMBytes, err := ioutil.ReadFile(config.Inputs.PublicKeyPath)
	if err != nil {
		return fmt.Errorf("failed to read public key %q: %s", config.Inputs.PublicKeyPath, err)
	}
	pubPEM, _ := pem.Decode(pubPEMBytes)
	if pubPEM == nil {
		return fmt.Errorf("failed to parse public key")
	}
	pub, err := x509.ParsePKIXPublicKey(pubPEM.Bytes)
	if err != nil {
		return fmt.Errorf("failed to parse public key: %s", err)
	}
	issuer, err := core.LoadCert(config.Inputs.IssuerCertificatePath)
	if err != nil {
		return fmt.Errorf("failed to load issuer certificate %q: %s", config.Inputs.IssuerCertificatePath, err)
	}

	template, err := makeTemplate(newRandReader(ctx, session), &config.CertProfile, pubPEM.Bytes, ct)
	if err != nil {
		return fmt.Errorf("failed to create certificate profile: %s", err)
	}
	template.AuthorityKeyId = issuer.SubjectKeyId

	err = signAndWriteCert(template, issuer, pub, signer, config.Outputs.CertificatePath)
	if err != nil {
		return err
	}

	return nil
}

func keyCeremony(configBytes []byte) error {
	var config keyConfig
	err := yaml.UnmarshalStrict(configBytes, &config)
	if err != nil {
		return fmt.Errorf("failed to parse config: %s", err)
	}
	if err := config.validate(); err != nil {
		return fmt.Errorf("failed to validate config: %s", err)
	}
	ctx, session, err := pkcs11helpers.Initialize(config.PKCS11.Module, config.PKCS11.StoreSlot, config.PKCS11.PIN)
	if err != nil {
		return fmt.Errorf("failed to setup session and PKCS#11 context for slot %d: %s", config.PKCS11.StoreSlot, err)
	}
	log.Printf("Opened PKCS#11 session for slot %d\n", config.PKCS11.StoreSlot)
	if _, err = generateKey(ctx, session, config.PKCS11.StoreLabel, config.Outputs.PublicKeyPath, config.Key); err != nil {
		return err
	}

	return nil
}

func ocspRespCeremony(configBytes []byte) error {
	var config ocspRespConfig
	err := yaml.UnmarshalStrict(configBytes, &config)
	if err != nil {
		return fmt.Errorf("failed to parse config: %s", err)
	}
	if err := config.validate(); err != nil {
		return fmt.Errorf("failed to validate config: %s", err)
	}

	ctx, session, err := pkcs11helpers.Initialize(config.PKCS11.Module, config.PKCS11.SigningSlot, config.PKCS11.PIN)
	if err != nil {
		return fmt.Errorf("failed to setup session and PKCS#11 context for slot %d: %s", config.PKCS11.SigningSlot, err)
	}
	log.Printf("Opened PKCS#11 session for slot %d\n", config.PKCS11.SigningSlot)
	keyID, err := hex.DecodeString(config.PKCS11.SigningKeyID)
	if err != nil {
		return fmt.Errorf("failed to decode key-id: %s", err)
	}
	signer, err := newSigner(ctx, session, config.PKCS11.SigningLabel, keyID)
	if err != nil {
		return fmt.Errorf("failed to retrieve private key handle: %s", err)
	}
	log.Println("Retrieved private key handle")

	cert, err := core.LoadCert(config.Inputs.CertificatePath)
	if err != nil {
		return fmt.Errorf("failed to load certificate %q: %s", config.Inputs.CertificatePath, err)
	}
	issuer, err := core.LoadCert(config.Inputs.IssuerCertificatePath)
	if err != nil {
		return fmt.Errorf("failed to load issuer certificate %q: %s", config.Inputs.IssuerCertificatePath, err)
	}
	var delegatedIssuer *x509.Certificate
	if config.Inputs.DelegatedIssuerCertificatePath != "" {
		delegatedIssuer, err = core.LoadCert(config.Inputs.DelegatedIssuerCertificatePath)
		if err != nil {
			return fmt.Errorf("failed to load delegated issuer certificate %q: %s", config.Inputs.DelegatedIssuerCertificatePath, err)
		}
	}

	thisUpdate, err := time.Parse(configDateLayout, config.OCSPProfile.ThisUpdate)
	if err != nil {
		return fmt.Errorf("unable to parse ocsp-profile.this-update: %s", err)
	}
	nextUpdate, err := time.Parse(configDateLayout, config.OCSPProfile.NextUpdate)
	if err != nil {
		return fmt.Errorf("unable to parse ocsp-profile.next-update: %s", err)
	}
	var status int
	switch config.OCSPProfile.Status {
	case "good":
		status = int(ocsp.Good)
	case "revoked":
		status = int(ocsp.Revoked)
	default:
		// this shouldn't happen if the config is validated
		return fmt.Errorf("unexpected ocsp-profile.stats: %s", config.OCSPProfile.Status)
	}

	resp, err := generateOCSPResponse(signer, issuer, delegatedIssuer, cert, thisUpdate, nextUpdate, status)
	if err != nil {
		return err
	}

	if err := ioutil.WriteFile(config.Outputs.ResponsePath, resp, 0644); err != nil {
		return fmt.Errorf("failed to write OCSP response to %q: %s", config.Outputs.ResponsePath, err)
	}

	return nil
}

func main() {
	configPath := flag.String("config", "", "Path to ceremony configuration file")
	flag.Parse()

	if *configPath == "" {
		log.Fatal("--config is required")
	}
	configBytes, err := ioutil.ReadFile(*configPath)
	if err != nil {
		log.Fatalf("Failed to read config file: %s", err)
	}
	var ct struct {
		CeremonyType string `yaml:"ceremony-type"`
	}
	err = yaml.Unmarshal(configBytes, &ct)
	if err != nil {
		log.Fatalf("Failed to parse config: %s", err)
	}

	switch ct.CeremonyType {
	case "root":
		err = rootCeremony(configBytes)
		if err != nil {
			log.Fatalf("root ceremony failed: %s", err)
		}
	case "intermediate":
		err = intermediateCeremony(configBytes, intermediateCert)
		if err != nil {
			log.Fatalf("intermediate ceremony failed: %s", err)
		}
	case "ocsp-signer":
		err = intermediateCeremony(configBytes, ocspCert)
		if err != nil {
			log.Fatalf("ocsp signer ceremony failed: %s", err)
		}
	case "key":
		err = keyCeremony(configBytes)
		if err != nil {
			log.Fatalf("key ceremony failed: %s", err)
		}
	case "ocsp-response":
		err = ocspRespCeremony(configBytes)
		if err != nil {
			log.Fatalf("ocsp response ceremony failed: %s", err)
		}
	case "crl-signer":
		err = intermediateCeremony(configBytes, crlCert)
		if err != nil {
			log.Fatalf("crl signer ceremony failed: %s", err)
		}
	default:
		log.Fatalf("unknown ceremony-type, must be one of: root, intermediate, ocsp-signer, crl-signer, key, ocsp-response")
	}
}
