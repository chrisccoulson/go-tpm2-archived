// Copyright 2019 Canonical Ltd.
// Licensed under the LGPLv3 with static-linking exception.
// See LICENCE file for details.

package tpm2

// Section 18 - Attestation Commands

// func (t *TPMContext) Certify(objectContext, signContext ResourceContext, qualifyingData Data,
//	inScheme *SigScheme, sessions ...*Session) (AttestRaw, *Signature, error) {
// }

// CertifyCreation executes the TPM2_CertifyCreation command, which is used to prove the association
// between the object represented by objectContext and its creation data represented by
// creationHash. It does this by computing a ticket from creationHash and the name of the object
// represented by objectContext and then verifying that it matches the provided creationTicket,
// which was provided by the TPM at object creation time.
//
// If successful, it returns an attestation structure. If signContext is not nil, the attestation
// structure will be signed by the associated key and returned separately.
func (t *TPMContext) CertifyCreation(signContext, objectContext ResourceContext, qualifyingData Data,
	creationHash Digest, inScheme *SigScheme, creationTicket *TkCreation,
	signContextAuth interface{}, sessions ...*Session) (AttestRaw, *Signature, error) {
	if signContext == nil {
		signContext = permanentContext(HandleNull)
	}
	if inScheme == nil {
		inScheme = &SigScheme{Scheme: AlgorithmNull}
	}

	var certifyInfo AttestRaw
	var signature Signature

	if err := t.RunCommand(CommandCertifyCreation, sessions,
		ResourceWithAuth{Context: signContext, Auth: signContextAuth}, objectContext, Separator,
		qualifyingData, creationHash, inScheme, creationTicket, Separator, Separator, &certifyInfo,
		&signature); err != nil {
		return nil, nil, err
	}

	return certifyInfo, &signature, nil
}

// func (t *TPMContext) Quote(signContext ResourceContext, qualifyingData Data, inScheme *SigScheme,
//	pcrSelection PCRSelectionList, session ...*Session) (AttestRaw, *Signature, error) {
// }

// func (t *TPMContext) GetSessionAuditDigest(privacyAdminHandle Handle, signContext,
//	sessionContext ResourceContext, qualifyingData Data, inScheme *SigScheme, sessions ...*Session) (AttestRaw,
//	*Signature, error) {
// }

// func (t *TPMContext) GetCommandAuditDigest(privacyHandle Handle, signContext ResourceContext,
//	qualifyingData Data, inScheme *SigScheme, sessions ...*Session) (AttestRaw, *Signature, error) {
// }

// func (t *TPMContext) GetTime(privacyAdminHandle Handle, signContext ResourceContext, qualifyingData Data,
//	inScheme *SigScheme, sessions ...*Session) (AttestRaw, *Signature, error) {
// }
