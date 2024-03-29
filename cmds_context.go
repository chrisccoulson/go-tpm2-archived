// Copyright 2019 Canonical Ltd.
// Licensed under the LGPLv3 with static-linking exception.
// See LICENCE file for details.

package tpm2

// Section 28 - Context Management

import (
	"bytes"
	"crypto"
	_ "crypto/sha256"
	"errors"
	"fmt"
)

func wrapContextBlob(tpmBlob ContextData, context HandleContext) ContextData {
	data, err := MarshalToBytes(context.(handleContextPrivate).data(), tpmBlob)
	if err != nil {
		panic(fmt.Sprintf("cannot marshal resource context and TPM context data: %v", err))
	}

	h := crypto.SHA256.New()
	h.Write(data)

	data, err = MarshalToBytes(HashAlgorithmSHA256, h.Sum(nil), data)
	if err != nil {
		panic(fmt.Sprintf("cannot marshal data blob and checksum: %v", err))
	}

	return data
}

// ContextSave executes the TPM2_ContextSave command on the handle referenced by saveContext, in order to save the context associated
// with that handle outside of the TPM. The TPM encrypts and integrity protects the context with a key derived from the hierarchy
// proof. If saveContext does not correspond to a transient object or a session, then it will return an error.
//
// On successful completion, it returns a Context instance that can be passed to TPMContext.ContextLoad. Note that this function
// wraps the context data returned from the TPM with some host-side state associated with the resource, so that it can be restored
// fully in TPMContext.ContextLoad. If saveContext corresponds to a session, the host-side state that is added to the returned context
// blob includes the session key.
//
// If saveContext corresponds to a session, then TPM2_ContextSave also removes resources associated with the session from the TPM
// (it becomes a saved session rather than a loaded session). In this case, saveContext is marked as not loaded and can only be used
// as an argument to TPMContext.FlushContext.
//
// If saveContext corresponds to a session and no more contexts can be saved, a *TPMError error will be returned with an error code
// of ErrorTooManyContexts. If a context ID cannot be assigned for the session, a *TPMWarning error with a warning code of
// WarningContextGap will be returned.
func (t *TPMContext) ContextSave(saveContext HandleContext) (*Context, error) {
	switch c := saveContext.(type) {
	case *sessionContext:
		if c.scData() == nil {
			return nil, makeInvalidParamError("saveContext", "unusable session HandleContext")
		}
	}

	var context Context

	if err := t.RunCommand(CommandContextSave, nil,
		saveContext, Delimiter,
		Delimiter,
		Delimiter,
		&context); err != nil {
		return nil, err
	}

	context.Blob = wrapContextBlob(context.Blob, saveContext)

	switch c := saveContext.(type) {
	case *sessionContext:
		c.d.Data.Data = (*sessionContextData)(nil)
	}

	return &context, nil
}

// ContextLoad executes the TPM2_ContextLoad command with the supplied Context, in order to restore a context previously saved from
// TPMContext.ContextSave.
//
// If the size field of the integrity HMAC in the context blob is greater than the size of the largest digest algorithm, a *TPMError
// with an error code of ErrorSize is returned. If the context blob is shorter than the size indicated for the integrity HMAC, a
// *TPMError with an error code of ErrorInsufficient is returned.
//
// If the size of the context's integrity HMAC does not match the context integrity digest algorithm for the TPM, or the context
// blob is too short, a *TPMParameterError error with an error code of ErrorSize will be returned. If the integrity HMAC check fails,
// a *TPMParameterError with an error code of ErrorIntegrity will be returned.
//
// If the hierarchy that the context is part of is disabled, a *TPMParameterError error with an error code of ErrorHierarchy will be
// returned.
//
// If the context corresponds to a session but the handle doesn't reference a saved session or the sequence number is invalid, a
// *TPMParameterError error with an error code of ErrorHandle will be returned.
//
// If the context corresponds to a session and no more sessions can be created until the oldest session is context loaded, and context
// doesn't correspond to the oldest session, a *TPMWarning error with a warning code of WarningContextGap will be returned.
//
// If there are no more slots available for objects or loaded sessions, a *TPMWarning error with a warning code of either
// WarningSessionMemory or WarningObjectMemory will be returned.
//
// On successful completion, it returns a HandleContext which corresponds to the resource loaded in to the TPM. If the context
// corresponds to an object, this will be a new ResourceContext. If context corresponds to a session, then this will be a new
// SessionContext.
func (t *TPMContext) ContextLoad(context *Context) (HandleContext, error) {
	if context == nil {
		return nil, makeInvalidParamError("context", "nil value")
	}

	var integrityAlg HashAlgorithmId
	var integrity []byte
	var data []byte
	if _, err := UnmarshalFromBytes(context.Blob, &integrityAlg, &integrity, &data); err != nil {
		return nil, fmt.Errorf("cannot load context: cannot unpack checksum and data blob: %v", err)
	}

	if !integrityAlg.Supported() {
		return nil, errors.New("cannot load context: invalid checksum algorithm")
	}
	h := integrityAlg.NewHash()
	h.Write(data)
	if !bytes.Equal(h.Sum(nil), integrity) {
		return nil, errors.New("cannot load context: invalid checksum")
	}

	var hcData *handleContextData
	var tpmBlob ContextData
	if _, err := UnmarshalFromBytes(data, &hcData, &tpmBlob); err != nil {
		return nil, fmt.Errorf("cannot load context: cannot unmarshal data blob: %v", err)
	}

	switch hcData.Type {
	case handleContextTypeObject, handleContextTypeSession:
	default:
		return nil, errors.New("cannot load context: unexpected context type")
	}
	if err := hcData.checkConsistency(); err != nil {
		return nil, fmt.Errorf("cannot load context: %v", err)
	}

	tpmContext := Context{
		Sequence:    context.Sequence,
		SavedHandle: context.SavedHandle,
		Hierarchy:   context.Hierarchy,
		Blob:        tpmBlob}

	var loadedHandle Handle

	if err := t.RunCommand(CommandContextLoad, nil,
		Delimiter,
		tpmContext, Delimiter,
		&loadedHandle); err != nil {
		return nil, err
	}

	switch hcData.Type {
	case handleContextTypeObject:
		if loadedHandle.Type() != HandleTypeTransient {
			return nil, &InvalidResponseError{CommandContextLoad, fmt.Sprintf("handle 0x%08x returned from TPM is the wrong type", loadedHandle)}
		}
		return makeObjectContext(loadedHandle, hcData.Name, hcData.Data.Data.(*Public)), nil
	case handleContextTypeSession:
		if loadedHandle != context.SavedHandle {
			return nil, &InvalidResponseError{CommandContextLoad, fmt.Sprintf("handle 0x%08x returned from TPM is incorrect", loadedHandle)}
		}
		sc := makeSessionContext(loadedHandle, hcData.Data.Data.(*sessionContextData))
		isExclusive := t.exclusiveSession != nil && loadedHandle == t.exclusiveSession.Handle()
		sc.scData().IsExclusive = isExclusive
		if isExclusive {
			t.exclusiveSession.scData().IsExclusive = false
			t.exclusiveSession = sc
		}
		return sc, nil
	default:
		panic("not reached")
	}
}

// FlushContext executes the TPM2_FlushContext command on the handle referenced by flushContext, in order to flush resources
// associated with it from the TPM. If flushContext does not correspond to a transient object or a session, then it will return
// with an error.
//
// On successful completion, flushContext is invalidated. If flushContext corresponded to a session, then it will no longer be
// possible to restore that session with TPMContext.ContextLoad, even if it was previously saved with TPMContext.ContextSave.
func (t *TPMContext) FlushContext(flushContext HandleContext) error {
	if err := t.checkHandleContextParam(flushContext); err != nil {
		return makeInvalidParamError("flushContext", fmt.Sprintf("%v", err))
	}

	if err := t.RunCommand(CommandFlushContext, nil,
		Delimiter,
		flushContext.Handle()); err != nil {
		return err
	}

	flushContext.(handleContextPrivate).invalidate()
	return nil
}

// EvictControl executes the TPM2_EvictControl command on the handle referenced by object. To persist a transient object,
// object should correspond to the transient object and persistentHandle should specify the persistent handle to which the
// resource associated with object should be persisted. To evict a persistent object, object should correspond to the
// persistent object and persistentHandle should be the handle associated with that resource.
//
// The auth parameter should be a ResourceContext that corresponds to a hierarchy - it should be HandlePlatform for objects within
// the platform hierarchy, or HandleOwner for objects within the storage or endorsement hierarchies. If auth is a ResourceContext
// corresponding to HandlePlatform but object corresponds to an object outside of the platform hierarchy, or auth is a ResourceContext
// corresponding to HandleOwner but object corresponds to an object inside of the platform hierarchy, a *TPMHandleError error with
// an error code of ErrorHierarchy will be returned for handle index 2. The auth handle requires authorization with the user auth
// role, with session based authorization provided via authAuthSession.
//
// If object corresponds to a transient object that only has a public part loaded, or which has the AttrStClear attribute set,
// then a *TPMHandleError error with an error code of ErrorAttributes will be returned for handle index 2.
//
// If object corresponds to a persistent object and persistentHandle is not the handle for that object, a *TPMHandleError error
// with an error code of ErrorHandle will be returned for handle index 2.
//
// If object corresponds to a transient object and persistentHandle is not in the correct range determined by the value of
// auth, a *TPMParameterError error with an error code of ErrorRange will be returned.
//
// If there is insuffient space to persist a transient object, a *TPMError error with an error code of ErrorNVSpace will be returned.
// If a persistent object already exists at the specified handle, a *TPMError error with an error code of ErrorNVDefined will be
// returned.
//
// On successful completion of persisting a transient object, it returns a ResourceContext that corresponds to the persistent object.
// On successful completion of evicting a persistent object, it returns a nil ResourceContext, and object will be invalidated.
func (t *TPMContext) EvictControl(auth, object ResourceContext, persistentHandle Handle, authAuthSession SessionContext, sessions ...SessionContext) (ResourceContext, error) {
	if object == nil {
		return nil, makeInvalidParamError("object", "nil value")
	}

	var public *Public
	if object.Handle() != persistentHandle {
		var err error
		public, err = object.(*objectContext).public().copy()
		if err != nil {
			return nil, fmt.Errorf("cannot copy public area of object: %v", err)
		}
	}

	if err := t.RunCommand(CommandEvictControl, sessions,
		ResourceContextWithSession{Context: auth, Session: authAuthSession}, object, Delimiter,
		persistentHandle); err != nil {
		return nil, err
	}

	if object.Handle() == persistentHandle {
		object.(handleContextPrivate).invalidate()
		return nil, nil
	}

	return makeObjectContext(persistentHandle, object.Name(), public), nil
}
