package proxy

import (
	"context"
	"errors"

	"github.com/godbus/dbus/v5"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/logging"
)

// ItemHandler handles Item interface calls for item objects.
// It is exported as a subtree handler for /org/freedesktop/secrets/collection/*/*.
type ItemHandler struct {
	properties
}

// Items under a collection's name. Items under an alias path are not served.
type itemObjects struct{}

// Checks whether the given path names an item.
func (itemObjects) serves(path dbus.ObjectPath) bool {
	ref, ok := dbustypes.ParseItemPath(path)
	return ok && !ref.Alias
}

func (o itemObjects) object(msg dbus.Message, _ dbustypes.Interface) (dbus.ObjectPath, *dbus.Error) {
	return calledPathIn(o, msg)
}

// Delete deletes the item.
// Signature: Delete() -> (prompt ObjectPath)
func (i *ItemHandler) Delete(msg dbus.Message) (dbus.ObjectPath, *dbus.Error) {
	path, dbusErr := i.object(msg, dbustypes.ItemInterface)
	if dbusErr != nil {
		return "/", dbusErr
	}

	caller, release := i.startApprovalCaller(msg)
	defer release()

	info := approval.RequestInfo{Items: []approval.ItemInfo{caller.itemInfo(path)}, Type: approval.RequestTypeDelete}
	upstreamCall, err := caller.requireApproval(info)
	if err != nil {
		i.logger.LogItemDelete(caller.ctx, path, logging.ResultDenied, err)
		return "/", dbustypes.ErrAccessDenied(err.Error())
	}

	call := i.callUpstream(upstreamCall, path, dbustypes.ItemInterface.Member("Delete"))
	var prompt dbus.ObjectPath
	if err := call.Store(&prompt); err != nil {
		return "/", dbustypes.ErrFailed(err)
	}

	i.logger.LogItemDelete(context.Background(), path, logging.ResultOK, nil)

	return prompt, nil
}

// GetSecret retrieves the secret for this item.
// Signature: GetSecret(session ObjectPath) -> (secret Secret)
func (i *ItemHandler) GetSecret(msg dbus.Message, session dbus.ObjectPath) (dbustypes.Secret, *dbus.Error) {
	path, dbusErr := i.object(msg, dbustypes.ItemInterface)
	if dbusErr != nil {
		return dbustypes.Secret{}, dbusErr
	}

	caller, release := i.startApprovalCaller(msg)
	defer release()

	info := approval.RequestInfo{Items: []approval.ItemInfo{caller.itemInfo(path)}, Session: session, Type: approval.RequestTypeGetSecret}
	upstreamCall, err := caller.requireApproval(info)
	if err != nil {
		i.logger.LogItemGetSecret(caller.ctx, path, logging.ResultDenied, err)
		return dbustypes.Secret{}, dbustypes.ErrAccessDenied(err.Error())
	}

	localSession, ok := i.sessions.GetLocalSession(session)
	if !ok {
		i.logger.LogItemGetSecret(context.Background(), path, logging.ResultError, dbustypes.ErrSessionNotFound(string(session)))
		return dbustypes.Secret{}, dbustypes.ErrSessionNotFound(string(session))
	}

	call := i.callUpstream(upstreamCall, path, dbustypes.ItemInterface.Member("GetSecret"), localSession)
	var secret dbustypes.Secret
	if err := call.Store(&secret); err != nil {
		i.logger.LogItemGetSecret(context.Background(), path, logging.ResultError, err)
		return dbustypes.Secret{}, dbustypes.ErrFailed(err)
	}

	// Rewrite session path and, for DH sessions, encrypt the value for the client.
	encoded, err := i.sessions.ForClient(session, secret)
	if err != nil {
		i.logger.LogItemGetSecret(context.Background(), path, logging.ResultError, err)
		return dbustypes.Secret{}, sessionSecretError(session, err)
	}

	i.logger.LogItemGetSecret(context.Background(), path, logging.ResultOK, nil)
	return encoded, nil
}

// SetSecret sets the secret for this item.
// Signature: SetSecret(secret Secret)
func (i *ItemHandler) SetSecret(msg dbus.Message, secret dbustypes.Secret) *dbus.Error {
	path, dbusErr := i.object(msg, dbustypes.ItemInterface)
	if dbusErr != nil {
		return dbusErr
	}

	caller, release := i.startApprovalCaller(msg)
	defer release()

	info := approval.RequestInfo{Items: []approval.ItemInfo{caller.itemInfo(path)}, Session: secret.Session, Type: approval.RequestTypeWrite}
	upstreamCall, err := caller.requireApproval(info)
	if err != nil {
		if errors.Is(err, approval.ErrIgnored) {
			i.logger.LogItemSetSecret(caller.ctx, path, logging.ResultIgnored, nil)
			return nil
		}
		i.logger.LogItemSetSecret(caller.ctx, path, logging.ResultDenied, err)
		return dbustypes.ErrAccessDenied(err.Error())
	}

	// Map the remote session to the upstream session, decrypting the value for
	// DH sessions before forwarding it to the (plain) upstream service.
	localSecret, err := i.sessions.ForUpstream(secret)
	if err != nil {
		i.logger.LogItemSetSecret(caller.ctx, path, logging.ResultError, err)
		return sessionSecretError(secret.Session, err)
	}

	call := i.callUpstream(upstreamCall, path, dbustypes.ItemInterface.Member("SetSecret"), localSecret)
	if call.Err != nil {
		return dbustypes.ErrFailed(call.Err)
	}

	i.logger.LogItemSetSecret(context.Background(), path, logging.ResultOK, nil)

	return nil
}
