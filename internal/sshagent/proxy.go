// Package sshagent implements an SSH agent proxy that gates SIGN_REQUEST
// operations through the approval flow.
package sshagent

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"sync/atomic"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

// An upstream SSH agent whose Sign requests go through the approval manager
// first.
type proxyAgent struct {
	upstream   agent.Agent
	approval   *approval.Manager
	senderInfo approval.SenderInfo
	// The host the client connects to; nil when it is not known.
	destination *string
	logger      *slog.Logger

	// The keys of the latest List. A sign request takes its key's comment from
	// them.
	keys atomic.Pointer[[]*agent.Key]
}

func newProxyAgent(upstream agent.Agent, approvalMgr *approval.Manager, senderInfo approval.SenderInfo, destination *string, logger *slog.Logger) *proxyAgent {
	p := &proxyAgent{
		upstream:    upstream,
		approval:    approvalMgr,
		senderInfo:  senderInfo,
		destination: destination,
		logger:      logger,
	}
	p.keys.Store(&[]*agent.Key{})
	return p
}

func (p *proxyAgent) List() ([]*agent.Key, error) {
	keys, err := p.upstream.List()
	if err != nil {
		return nil, err
	}
	p.keys.Store(&keys)
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
	item := approval.SSHSignItem(fingerprint, comment, p.destination)

	p.logger.Info("sign request received",
		"fingerprint", fingerprint,
		"comment", comment,
		"destination", item.SSHDestination(),
		"invoker", p.senderInfo.InvokerName)

	info := approval.RequestInfo{Items: []approval.ItemInfo{item}, Type: approval.RequestTypeSSHSign, SenderInfo: p.senderInfo}
	if _, err := p.approval.RequireApproval(context.TODO(), info); err != nil {
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

// Returns the comment of key, matched by its blob against the cached key
// list.
func (p *proxyAgent) findKeyComment(key ssh.PublicKey) string {
	blob := key.Marshal()
	h := sha256.Sum256(blob)

	for _, k := range *p.keys.Load() {
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
