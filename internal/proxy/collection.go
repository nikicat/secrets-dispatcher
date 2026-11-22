package proxy

import (
	"context"
	"errors"

	"github.com/godbus/dbus/v5"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/logging"
)

// CollectionHandler handles Collection interface calls for collection objects.
// It is exported as a subtree handler for /org/freedesktop/secrets/collection/*.
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

// Delete deletes the collection.
// Signature: Delete() -> (prompt ObjectPath)
func (c *CollectionHandler) Delete(msg dbus.Message) (dbus.ObjectPath, *dbus.Error) {
	path, dbusErr := c.object(msg, dbustypes.CollectionInterface)
	if dbusErr != nil {
		return "/", dbusErr
	}

	caller, release := c.startApprovalCaller(msg)
	defer release()

	info := approval.RequestInfo{Items: []approval.ItemInfo{caller.collectionInfo(path)}, Type: approval.RequestTypeDelete}
	upstreamCall, err := caller.requireApproval(info)
	if err != nil {
		c.logger.LogCollectionDelete(caller.ctx, path, logging.ResultDenied, err)
		return "/", dbustypes.ErrAccessDenied(err.Error())
	}

	call := c.callUpstream(upstreamCall, path, dbustypes.CollectionInterface.Member("Delete"))
	var prompt dbus.ObjectPath
	if err := call.Store(&prompt); err != nil {
		return "/", dbustypes.ErrFailed(err)
	}

	c.logger.LogCollectionDelete(context.Background(), path, logging.ResultOK, nil)

	return prompt, nil
}

// SearchItems searches for items in this collection matching the given attributes.
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

// CreateItem creates a new item in the collection.
// Signature: CreateItem(properties Dict<String,Variant>, secret Secret, replace Boolean) -> (item ObjectPath, prompt ObjectPath)
func (c *CollectionHandler) CreateItem(msg dbus.Message, properties map[string]dbus.Variant, secret dbustypes.Secret, replace bool) (dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	path, dbusErr := c.object(msg, dbustypes.CollectionInterface)
	if dbusErr != nil {
		return "/", "/", dbusErr
	}

	itemInfo := itemInfoFromProperties(string(path), properties)

	caller, release := c.startApprovalCaller(msg)
	defer release()

	info := approval.RequestInfo{Items: []approval.ItemInfo{itemInfo}, Session: secret.Session, Type: approval.RequestTypeWrite}
	upstreamCall, err := caller.requireApproval(info)
	if errors.Is(err, approval.ErrIgnored) {
		c.logger.LogCreateItem(caller.ctx, path, logging.ResultIgnored, nil)
		return "/", "/", nil
	}
	if err != nil {
		c.logger.LogCreateItem(caller.ctx, path, logging.ResultDenied, err)
		return "/", "/", dbustypes.ErrAccessDenied(err.Error())
	}

	// Map the remote session to the upstream session, decrypting the value for
	// DH sessions before forwarding it to the (plain) upstream service.
	localSecret, err := c.sessions.ForUpstream(secret)
	if err != nil {
		c.logger.LogCreateItem(caller.ctx, path, logging.ResultError, err)
		return "/", "/", sessionSecretError(secret.Session, err)
	}

	call := c.callUpstream(upstreamCall, path, dbustypes.CollectionInterface.Member("CreateItem"), properties, localSecret, replace)
	var item, prompt dbus.ObjectPath
	if err := call.Store(&item, &prompt); err != nil {
		return "/", "/", dbustypes.ErrFailed(err)
	}

	// Cache the new item path so immediate read-back (e.g., gh verification) is auto-approved.
	c.approval.CacheItemForSender(caller.sender, string(item))

	c.logger.LogItemCreated(context.Background(), path, item)

	return item, prompt, nil
}
