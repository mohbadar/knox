package server

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/pinterest/knox"
	"github.com/pinterest/knox/server/auth"
)

var routes = [...]route{
	route{
		method:  "GET",
		id:      "getkeys",
		path:    "/v0/keys/",
		handler: getKeysHandler,
		parameters: []parameter{
			rawQueryParameter("queryString"),
		},
	},
	route{
		method:  "POST",
		id:      "postkeys",
		path:    "/v0/keys/",
		handler: postKeysHandler,
		parameters: []parameter{
			postParameter("id"),
			postParameter("data"),
			postParameter("acl"),
		},
	},

	route{
		method:  "GET",
		id:      "getkey",
		path:    "/v0/keys/{keyID}/",
		handler: getKeyHandler,
		parameters: []parameter{
			urlParameter("keyID"),
			queryParameter("status"),
		},
	},
	route{
		method:  "DELETE",
		id:      "deletekey",
		path:    "/v0/keys/{keyID}/",
		handler: deleteKeyHandler,
		parameters: []parameter{
			urlParameter("keyID"),
		},
	},
	route{
		method:  "GET",
		id:      "getaccess",
		path:    "/v0/keys/{keyID}/access/",
		handler: getAccessHandler,
		parameters: []parameter{
			urlParameter("keyID"),
		},
	},
	route{
		method:  "PUT",
		id:      "putaccess",
		path:    "/v0/keys/{keyID}/access/",
		handler: putAccessHandler,
		parameters: []parameter{
			urlParameter("keyID"),
			postParameter("access"),
		},
	},
	route{
		method:  "POST",
		id:      "postversion",
		path:    "/v0/keys/{keyID}/versions/",
		handler: postVersionHandler,
		parameters: []parameter{
			urlParameter("keyID"),
			postParameter("data"),
		},
	},
	route{
		method:  "PUT",
		id:      "putversion",
		path:    "/v0/keys/{keyID}/versions/{versionID}/",
		handler: putVersionsHandler,
		parameters: []parameter{
			urlParameter("keyID"),
			urlParameter("versionID"),
			postParameter("status"),
		},
	},
}

// getKeysHandler is a handler that gets key IDs specified in the request.
//
// This returns all keys if no keyIds are passed in. Otherwise it returns the requested Key IDs that have been changed.
// It is used for both discovering what keys are available and for finding which keys have updates available. Keys are passed in as url parameters.
// This is going to have url length problems when a large number of keys are
// requested. A proposed fix is to just use the request body but that violates
// REST so that fix will be postponed until this actually is a problem.
// The route for this handler is GET /v0/keys/
// There are no authorization constraints on this route.
func getKeysHandler(m KeyManager, principal knox.Principal, parameters map[string]string) (interface{}, *httpError) {
	queryString := parameters["queryString"]

	// Can't throw error since direct from a http request
	keyMap, _ := url.ParseQuery(queryString)
	keyM := map[string]string{}
	for k := range keyMap {
		for _, v := range keyMap[k] {
			keyM[k] = v
		}
	}

	// Get necessary data based on parameters
	if len(keyMap) == 0 {
		keys, err := m.GetAllKeyIDs()
		if err != nil {
			return nil, errF(knox.InternalServerErrorCode, err.Error())
		}
		return keys, nil
	}

	keys, err := m.GetUpdatedKeyIDs(keyM)
	if err != nil {
		return nil, errF(knox.InternalServerErrorCode, err.Error())
	}
	return keys, nil
}

// postKeysHandler creates a new key and stores it. It reads from the post data
// key ID, base64 encoded data, and JSON encoded ACL.
// It returns the key version ID of the original Primary key version.
// The route for this handler is POST /v0/keys/
// The postKeysHandler must be a User.
func postKeysHandler(m KeyManager, principal knox.Principal, parameters map[string]string) (interface{}, *httpError) {

	// Authorize
	if !auth.IsUser(principal) {
		return nil, errF(knox.UnauthorizedCode, "")
	}

	keyID, keyIDOK := parameters["id"]
	if !keyIDOK {
		return nil, errF(knox.NoKeyIDCode, "")
	}
	data, dataOK := parameters["data"]
	if !dataOK {
		return nil, errF(knox.NoKeyDataCode, "")
	}
	aclStr, aclOK := parameters["acl"]

	acl := make(knox.ACL, 0)
	if aclOK {
		jsonErr := json.Unmarshal([]byte(aclStr), &acl)
		if jsonErr != nil {
			return nil, errF(knox.BadRequestDataCode, jsonErr.Error())
		}
	}

	decodedData, decodeErr := base64.StdEncoding.DecodeString(data)
	if decodeErr != nil {
		return nil, errF(knox.BadRequestDataCode, decodeErr.Error())
	}

	// Create and add new key
	key := newKey(keyID, acl, decodedData, principal)
	err := m.AddNewKey(&key)
	if err != nil {
		if err == knox.ErrKeyExists {
			return nil, errF(knox.KeyIdentifierExistsCode, "")
		}
		if err == knox.ErrInvalidKeyID {
			return nil, errF(knox.BadKeyFormatCode, "")
		}

		return nil, errF(knox.InternalServerErrorCode, err.Error())
	}
	return key.VersionList[0].ID, nil
}

// getKeyHandler gets the key matching the keyID in the request.
// The route for this handler is GET /v0/keys/<key_id>/
// The principal must have Read access to the key
func getKeyHandler(m KeyManager, principal knox.Principal, parameters map[string]string) (interface{}, *httpError) {
	keyID := parameters["keyID"]

	status := knox.Active
	statusStr, statusOK := parameters["status"]
	if statusOK {
		statusErr := status.UnmarshalJSON([]byte(statusStr))
		if statusErr != nil {
			return nil, errF(knox.BadRequestDataCode, statusErr.Error())
		}
	}

	// Get data
	key, getErr := m.GetKey(keyID, status)
	if getErr != nil {
		if getErr == knox.ErrKeyIDNotFound {
			return nil, errF(knox.KeyIdentifierDoesNotExistCode, "")
		}
		return nil, errF(knox.InternalServerErrorCode, getErr.Error())
	}

	// Authorize access to data
	if !principal.CanAccess(key.ACL, knox.Read) {
		return nil, errF(knox.UnauthorizedCode, "")
	}
	return key, nil
}

// deleteKeyHandler deletes the key matching the keyID in the request.
// The route for this handler is DELETE /v0/keys/<key_id>/
// The principal needs Admin access to the key.
func deleteKeyHandler(m KeyManager, principal knox.Principal, parameters map[string]string) (interface{}, *httpError) {
	keyID := parameters["keyID"]

	key, getErr := m.GetKey(keyID, knox.Primary)
	if getErr != nil {
		if getErr == knox.ErrKeyIDNotFound {
			return nil, errF(knox.KeyIdentifierDoesNotExistCode, "")
		}
		return nil, errF(knox.InternalServerErrorCode, getErr.Error())
	}

	// Authorize
	if !principal.CanAccess(key.ACL, knox.Admin) {
		return nil, errF(knox.UnauthorizedCode, "")
	}

	// Delete the key
	err := m.DeleteKey(keyID)
	if err != nil {
		return nil, errF(knox.InternalServerErrorCode, err.Error())
	}
	return nil, nil
}

// getAccessHandler gets the ACL for a specific Key.
// The route for this handler is GET /v0/keys/<key_id>/access/
func getAccessHandler(m KeyManager, principal knox.Principal, parameters map[string]string) (interface{}, *httpError) {

	keyID := parameters["keyID"]

	// Get the key
	key, getErr := m.GetKey(keyID, knox.Primary)
	if getErr != nil {
		if getErr == knox.ErrKeyIDNotFound {
			return nil, errF(knox.KeyIdentifierDoesNotExistCode, "")
		}
		return nil, errF(knox.InternalServerErrorCode, getErr.Error())
	}

	// NO authorization on purpose
	// this allows, e.g., to see who has admin access to ask for grants

	return key.ACL, nil
}

// putAccessHandler adds or updates the existing ACL with an Access object
// This object is input as base64 encoded json encoded form data
// The route for this handler is PUT /v0/keys/<key_id>/access/
// The principal needs Admin access.
func putAccessHandler(m KeyManager, principal knox.Principal, parameters map[string]string) (interface{}, *httpError) {
	keyID := parameters["keyID"]

	accessStr, accessOK := parameters["access"]
	if !accessOK {
		return nil, errF(knox.BadRequestDataCode, "")
	}
	access := knox.Access{}

	// If JSON decode fails, try a base64 encoded JSON string (both options are around for backwards compatibility)
	jsonErr := json.Unmarshal([]byte(accessStr), &access)
	if jsonErr != nil {
		decodedData, decodeErr := base64.RawURLEncoding.DecodeString(accessStr)
		if decodeErr != nil {
			return nil, errF(knox.BadRequestDataCode, decodeErr.Error())
		}
		jsonErr := json.Unmarshal(decodedData, &access)
		if jsonErr != nil {
			return nil, errF(knox.BadRequestDataCode, jsonErr.Error())
		}
	}

	// Get the Key
	key, getErr := m.GetKey(keyID, knox.Primary)
	if getErr != nil {
		if getErr == knox.ErrKeyIDNotFound {
			return nil, errF(knox.KeyIdentifierDoesNotExistCode, "")
		}
		return nil, errF(knox.InternalServerErrorCode, getErr.Error())
	}

	// Authorize
	if !principal.CanAccess(key.ACL, knox.Admin) {
		return nil, errF(knox.UnauthorizedCode, "")
	}

	//Deny addition of ACLS with empty MachinePrefix for Read,Write,and Admin AccessType
	//Empty MachinePrefix is allowed with None AccessType for revoking existing ACLS
	if access.Type == knox.MachinePrefix  && access.ID == "" && access.AccessType != knox.None{
                return nil, errF(knox.BadRequestDataCode,knox.ErrACLEmptyMachinePrefix.Error())
	}

	// Update Access
	updateErr := m.UpdateAccess(keyID, access)
	if updateErr != nil {
		return nil, errF(knox.InternalServerErrorCode, updateErr.Error())
	}
	return nil, nil
}

// postVersionHandler creates a new key version. This version is immediately
// added as an Active key.
// The route for this handler is PUT /v0/keys/<key_id>/versions/
// The principal needs Write access.
func postVersionHandler(m KeyManager, principal knox.Principal, parameters map[string]string) (interface{}, *httpError) {

	keyID := parameters["keyID"]
	dataStr, dataOK := parameters["data"]
	if !dataOK {
		return nil, errF(knox.BadRequestDataCode, "")
	}
	decodedData, decodeErr := base64.StdEncoding.DecodeString(dataStr)
	if decodeErr != nil {
		return nil, errF(knox.BadRequestDataCode, decodeErr.Error())
	}

	// Get the key
	key, getErr := m.GetKey(keyID, knox.Inactive)
	if getErr != nil {
		if getErr == knox.ErrKeyIDNotFound {
			return nil, errF(knox.KeyIdentifierDoesNotExistCode, "")
		}
		return nil, errF(knox.InternalServerErrorCode, getErr.Error())
	}

	// Authorize
	if !principal.CanAccess(key.ACL, knox.Write) {
		return nil, errF(knox.UnauthorizedCode, "")
	}

	// Create and add the new version
	version := newKeyVersion(decodedData, knox.Active)

	err := m.AddVersion(keyID, &version)

	if err != nil {
		return nil, errF(knox.InternalServerErrorCode, err.Error())
	}
	return version.ID, nil
}

// putVersionsHandler rotates key versions by changing the version status.
// It takes the new status as input. Accepted inputs include:
// If the key version is Inactive, it can become Active.
// If the key version is Active, it can become Inactive or Primary. Note that,
//   this will change the current Primary key to Active.
// If the key version is Primary, the version status cannot be changed. Instead
//   promote another key version to Primary to replace it.
// The route for this handler is PUT /v0/keys/<key_id>/versions/<version_id>/
// The principal needs Write access.
func putVersionsHandler(m KeyManager, principal knox.Principal, parameters map[string]string) (interface{}, *httpError) {

	keyID := parameters["keyID"]
	versionID := parameters["versionID"]

	statusStr, statusOK := parameters["status"]
	if !statusOK {
		return nil, errF(knox.BadRequestDataCode, "")
	}
	status := knox.Active
	statusErr := status.UnmarshalJSON([]byte(statusStr))
	if statusErr != nil {
		return nil, errF(knox.BadRequestDataCode, statusErr.Error())
	}
	id, intErr := strconv.ParseUint(versionID, 10, 64)
	if intErr != nil {
		return nil, errF(knox.BadRequestDataCode, intErr.Error())
	}

	// Get the key
	key, getErr := m.GetKey(keyID, knox.Inactive)
	if getErr != nil {
		if getErr == knox.ErrKeyIDNotFound {
			return nil, errF(knox.KeyIdentifierDoesNotExistCode, "")
		}
		return nil, errF(knox.InternalServerErrorCode, getErr.Error())
	}

	// Authorize
	if !principal.CanAccess(key.ACL, knox.Write) {
		return nil, errF(knox.UnauthorizedCode, "")
	}

	err := m.UpdateVersion(keyID, id, status)

	switch err {
	case nil:
		return nil, nil
	case knox.ErrKeyVersionNotFound:
		return nil, errF(knox.KeyVersionDoesNotExistCode, err.Error())
	case knox.ErrPrimaryToInactive, knox.ErrPrimaryToActive, knox.ErrInactiveToPrimary:
		return nil, errF(knox.BadRequestDataCode, err.Error())
	default:
		return nil, errF(knox.InternalServerErrorCode, err.Error())
	}
}
