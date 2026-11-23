package proxy

import (
	"context"
	"errors"

	"github.com/godbus/dbus/v5"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/logging"
)

// Serves org.freedesktop.Secret.Collection.
type CollectionHandler struct {
	properties
}

// Collections, by their name or by an alias.
type collectionObjects struct{}

// Checks whether the given path names a collection.
func (collectionObjects) serves(path dbus.ObjectPath) bool {
	_, ok := dbustypes.ParseCollectionPath(path)
	return ok
}

func (o collectionObjects) object(msg dbus.Message, _ dbustypes.Interface) (dbus.ObjectPath, *dbus.Error) {
	return calledPathIn(o, msg)
}

// Deletes the collection when the request is approved. Errors with AccessDenied
// when it is denied or expires.
// Signature: Delete() -> (prompt ObjectPath)
func (c *CollectionHandler) Delete(msg dbus.Message) (dbus.ObjectPath, *dbus.Error) {
	path, dbusErr := c.object(msg, dbustypes.CollectionInterface)
	if dbusErr != nil {
		return dbustypes.NoObject, dbusErr
	}

	caller, release := c.startApprovalCaller(msg)
	defer release()

	info := approval.RequestInfo{Items: []approval.ItemInfo{caller.collectionInfo(path)}, Type: approval.RequestTypeDelete}
	upstreamCall, err := caller.requireApproval(info)
	if err != nil {
		c.logger.LogCollectionDelete(caller.ctx, path, logging.ResultDenied, err)
		return dbustypes.NoObject, dbustypes.ErrAccessDenied(err.Error())
	}

	call := c.callUpstream(upstreamCall, path, dbustypes.CollectionInterface.Member("Delete"))
	var prompt dbus.ObjectPath
	if err := call.Store(&prompt); err != nil {
		return dbustypes.NoObject, dbustypes.ErrFailed(err)
	}

	c.logger.LogCollectionDelete(context.Background(), path, logging.ResultOK, nil)

	return prompt, nil
}

// Returns the items in the collection whose attributes match the given ones.
// Signature: SearchItems(attributes Dict<String,String>) -> (results Array<ObjectPath>)
func (c *CollectionHandler) SearchItems(msg dbus.Message, attributes map[string]string) ([]dbus.ObjectPath, *dbus.Error) {
	path, dbusErr := c.object(msg, dbustypes.CollectionInterface)
	if dbusErr != nil {
		return nil, dbusErr
	}

	upstreamCall, err := c.caller(msg).passthrough(searchRequest(attributes))
	if err != nil {
		return nil, dbustypes.ErrAccessDenied(err.Error())
	}

	call := c.callUpstream(upstreamCall, path, dbustypes.CollectionInterface.Member("SearchItems"), attributes)
	var results []dbus.ObjectPath
	if err := call.Store(&results); err != nil {
		return nil, dbustypes.ErrFailed(err)
	}

	c.logger.LogCollectionSearchItems(context.Background(), path, attributes, len(results), logging.ResultOK, nil)

	return results, nil
}

// Creates an item in the collection when the request is approved. An ignored
// write (ADR 0005) succeeds with no item and writes nothing. Errors with
// InvalidArgs on a mistyped property, AccessDenied on a denied or expired request.
// Signature: CreateItem(properties Dict<String,Variant>, secret Secret, replace Boolean) -> (item ObjectPath, prompt ObjectPath)
func (c *CollectionHandler) CreateItem(msg dbus.Message, properties map[string]dbus.Variant, secret dbustypes.Secret, replace bool) (dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	path, dbusErr := c.object(msg, dbustypes.CollectionInterface)
	if dbusErr != nil {
		return dbustypes.NoObject, dbustypes.NoObject, dbusErr
	}

	caller, release := c.startApprovalCaller(msg)
	defer release()

	itemInfo, err := itemInfoFromProperties(string(path), properties)
	if err != nil {
		c.logger.LogCreateItem(caller.ctx, path, logging.ResultError, err)
		return dbustypes.NoObject, dbustypes.NoObject, dbustypes.AsDBusError(err)
	}

	info := approval.RequestInfo{Items: []approval.ItemInfo{itemInfo}, Session: secret.Session, Type: approval.RequestTypeWrite}
	upstreamCall, err := caller.requireApproval(info)
	if errors.Is(err, approval.ErrIgnored) {
		c.logger.LogCreateItem(caller.ctx, path, logging.ResultIgnored, nil)
		return dbustypes.NoObject, dbustypes.NoObject, nil
	}
	if err != nil {
		c.logger.LogCreateItem(caller.ctx, path, logging.ResultDenied, err)
		return dbustypes.NoObject, dbustypes.NoObject, dbustypes.ErrAccessDenied(err.Error())
	}

	upstreamSecret, err := c.sessions.ForUpstream(secret)
	if err != nil {
		c.logger.LogCreateItem(caller.ctx, path, logging.ResultError, err)
		return dbustypes.NoObject, dbustypes.NoObject, sessionSecretError(secret.Session, err)
	}

	call := c.callUpstream(upstreamCall, path, dbustypes.CollectionInterface.Member("CreateItem"), properties, upstreamSecret, replace)
	var item, prompt dbus.ObjectPath
	if err := call.Store(&item, &prompt); err != nil {
		return dbustypes.NoObject, dbustypes.NoObject, dbustypes.ErrFailed(err)
	}

	// Cache the new item path so immediate read-back (e.g., gh verification) is auto-approved.
	c.approval.CacheItemForSender(caller.sender, string(item))

	c.logger.LogItemCreated(context.Background(), path, item)

	return item, prompt, nil
}
