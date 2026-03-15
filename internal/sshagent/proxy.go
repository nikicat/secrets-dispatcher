// Package sshagent implements an SSH agent proxy that gates SIGN_REQUEST
// operations through the approval flow.
package sshagent

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

// proxyAgent wraps an upstream SSH agent, delegating most operations but
// gating Sign requests through the approval manager.
type proxyAgent struct {
	upstream    agent.Agent
	approval    *approval.Manager
	senderInfo  approval.SenderInfo
	destination string // resolved SSH destination host
	logger      *slog.Logger

	// cache key list so we can look up comments for sign requests
	keysMu sync.Mutex
	keys   []*agent.Key
}

func newProxyAgent(upstream agent.Agent, approvalMgr *approval.Manager, senderInfo approval.SenderInfo, destination string, logger *slog.Logger) *proxyAgent {
	return &proxyAgent{
		upstream:    upstream,
		approval:    approvalMgr,
		senderInfo:  senderInfo,
		destination: destination,
		logger:      logger,
	}
}

func (p *proxyAgent) List() ([]*agent.Key, error) {
	keys, err := p.upstream.List()
	if err != nil {
		return nil, err
	}
	p.keysMu.Lock()
	p.keys = keys
	p.keysMu.Unlock()
	return keys, nil
}

func (p *proxyAgent) Sign(key ssh.PublicKey, data []byte) (*ssh.Signature, error) {
	return p.signWithApproval(key, data, 0)
}

func (p *proxyAgent) SignWithFlags(key ssh.PublicKey, data []byte, flags agent.SignatureFlags) (*ssh.Signature, error) {
	return p.signWithApproval(key, data, flags)
}

func (p *proxyAgent) signWithApproval(key ssh.PublicKey, data []byte, flags agent.SignatureFlags) (*ssh.Signature, error) {
	fingerprint := ssh.FingerprintSHA256(key)
	comment := p.findKeyComment(key)
	label := comment
	if label == "" {
		label = fingerprint
	}

	attrs := map[string]string{
		"fingerprint": fingerprint,
	}
	if p.destination != "" {
		attrs["destination"] = p.destination
	}

	items := []approval.ItemInfo{{
		Path:       fingerprint,
		Label:      label,
		Attributes: attrs,
	}}

	p.logger.Info("sign request received",
		"fingerprint", fingerprint,
		"comment", comment,
		"destination", p.destination,
		"invoker", p.senderInfo.UnitName)

	if _, err := p.approval.RequireApproval(
		context.TODO(),
		"ssh-agent",
		items,
		"",
		approval.RequestTypeSSHSign,
		nil,
		p.senderInfo,
	); err != nil {
		p.logger.Info("sign request denied", "fingerprint", fingerprint, "error", err)
		return nil, fmt.Errorf("sign request denied: %w", err)
	}

	p.logger.Info("sign request approved", "fingerprint", fingerprint)
	if flags != 0 {
		if ext, ok := p.upstream.(agent.ExtendedAgent); ok {
			return ext.SignWithFlags(key, data, flags)
		}
	}
	return p.upstream.Sign(key, data)
}

// findKeyComment looks up the comment for a key by matching its blob
// against the cached key list.
func (p *proxyAgent) findKeyComment(key ssh.PublicKey) string {
	blob := key.Marshal()
	h := sha256.Sum256(blob)

	p.keysMu.Lock()
	keys := p.keys
	p.keysMu.Unlock()

	for _, k := range keys {
		kh := sha256.Sum256(k.Marshal())
		if h == kh {
			return k.Comment
		}
	}
	return ""
}

// Passthrough methods — delegate directly to upstream.

func (p *proxyAgent) Add(key agent.AddedKey) error {
	return p.upstream.Add(key)
}

func (p *proxyAgent) Remove(key ssh.PublicKey) error {
	return p.upstream.Remove(key)
}

func (p *proxyAgent) RemoveAll() error {
	return p.upstream.RemoveAll()
}

func (p *proxyAgent) Lock(passphrase []byte) error {
	return p.upstream.Lock(passphrase)
}

func (p *proxyAgent) Unlock(passphrase []byte) error {
	return p.upstream.Unlock(passphrase)
}

func (p *proxyAgent) Signers() ([]ssh.Signer, error) {
	return p.upstream.Signers()
}

func (p *proxyAgent) Extension(extensionType string, contents []byte) ([]byte, error) {
	if ext, ok := p.upstream.(agent.ExtendedAgent); ok {
		return ext.Extension(extensionType, contents)
	}
	return nil, agent.ErrExtensionUnsupported
}
