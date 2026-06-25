package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"sync"
)

type DatadogStore struct {
	mu      sync.RWMutex
	path    string
	secrets map[string]string // installationID → secret
}

func NewDatadogStore(path string) (*DatadogStore, error) {
	ds := &DatadogStore{
		path:    path,
		secrets: make(map[string]string),
	}
	if err := ds.load(); err != nil {
		return nil, err
	}
	return ds, nil
}

func (ds *DatadogStore) Secret(installationID string) (string, bool) {
	ds.mu.RLock()
	defer ds.mu.RUnlock()
	s, ok := ds.secrets[installationID]
	return s, ok
}

func (ds *DatadogStore) Provision(installationID string) (string, error) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	if s, ok := ds.secrets[installationID]; ok {
		return s, nil
	}
	secret, err := generateSecret()
	if err != nil {
		return "", err
	}
	ds.secrets[installationID] = secret
	return secret, ds.save()
}

func (ds *DatadogStore) Rotate(installationID string) (string, error) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	secret, err := generateSecret()
	if err != nil {
		return "", err
	}
	ds.secrets[installationID] = secret
	return secret, ds.save()
}

func (ds *DatadogStore) InstallationBySecret(secret string) (string, bool) {
	ds.mu.RLock()
	defer ds.mu.RUnlock()
	for id, s := range ds.secrets {
		if s == secret {
			return id, true
		}
	}
	return "", false
}

func (ds *DatadogStore) load() error {
	data, err := os.ReadFile(ds.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &ds.secrets)
}

func (ds *DatadogStore) save() error {
	data, err := json.MarshalIndent(ds.secrets, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(ds.path, data, 0600)
}

func generateSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
