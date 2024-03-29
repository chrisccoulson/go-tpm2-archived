// Copyright 2019 Canonical Ltd.
// Licensed under the LGPLv3 with static-linking exception.
// See LICENCE file for details.

package tpm2

import (
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"encoding/binary"
	"fmt"
	"hash"
	"math/big"

	"github.com/chrisccoulson/go-tpm2/internal"
)

func getHashConstructor(alg HashAlgorithmId) func() hash.Hash {
	return func() hash.Hash {
		return alg.NewHash()
	}
}

func eccCurveToGoCurve(curve ECCCurve) elliptic.Curve {
	switch curve {
	case ECCCurveNIST_P224:
		return elliptic.P224()
	case ECCCurveNIST_P256:
		return elliptic.P256()
	case ECCCurveNIST_P384:
		return elliptic.P384()
	case ECCCurveNIST_P521:
		return elliptic.P521()
	}
	return nil
}

func cryptComputeCpHash(hashAlg HashAlgorithmId, commandCode CommandCode, commandHandles []Name,
	cpBytes []byte) []byte {
	hash := hashAlg.NewHash()

	binary.Write(hash, binary.BigEndian, commandCode)
	for _, name := range commandHandles {
		hash.Write([]byte(name))
	}
	hash.Write(cpBytes)

	return hash.Sum(nil)
}

func cryptComputeRpHash(hashAlg HashAlgorithmId, responseCode ResponseCode, commandCode CommandCode, rpBytes []byte) []byte {
	hash := hashAlg.NewHash()

	binary.Write(hash, binary.BigEndian, responseCode)
	binary.Write(hash, binary.BigEndian, commandCode)
	hash.Write(rpBytes)

	return hash.Sum(nil)
}

func computeSessionHMAC(alg HashAlgorithmId, key, pHash []byte, nonceNewer, nonceOlder, nonceDecrypt, nonceEncrypt Nonce,
	attrs sessionAttrs) []byte {
	hmac := hmac.New(getHashConstructor(alg), key)

	hmac.Write(pHash)
	hmac.Write(nonceNewer)
	hmac.Write(nonceOlder)
	hmac.Write(nonceDecrypt)
	hmac.Write(nonceEncrypt)
	hmac.Write([]byte{uint8(attrs)})

	return hmac.Sum(nil)
}

func cryptComputeSessionCommandHMAC(context *sessionContext, key, cpHash []byte, nonceDecrypt, nonceEncrypt Nonce, attrs sessionAttrs) []byte {
	scData := context.scData()
	return computeSessionHMAC(scData.HashAlg, key, cpHash, scData.NonceCaller, scData.NonceTPM, nonceDecrypt, nonceEncrypt, attrs)
}

func cryptComputeSessionResponseHMAC(context *sessionContext, key, rpHash []byte, attrs sessionAttrs) []byte {
	scData := context.scData()
	return computeSessionHMAC(scData.HashAlg, key, rpHash, scData.NonceTPM, scData.NonceCaller, nil, nil, attrs)
}

func cryptComputeNonce(nonce []byte) error {
	_, err := rand.Read(nonce)
	return err
}

func cryptEncryptRSA(public *Public, paddingOverride RSASchemeId, data, label []byte) ([]byte, error) {
	if public.Type != ObjectTypeRSA {
		panic(fmt.Sprintf("Unsupported key type %v", public.Type))
	}

	exp := int(public.Params.RSADetail().Exponent)
	if exp == 0 {
		exp = DefaultRSAExponent
	}
	pubKey := &rsa.PublicKey{N: new(big.Int).SetBytes(public.Unique.RSA()), E: exp}

	padding := public.Params.RSADetail().Scheme.Scheme
	if paddingOverride != RSASchemeNull {
		padding = paddingOverride
	}

	switch padding {
	case RSASchemeOAEP:
		schemeHashAlg := public.NameAlg
		if paddingOverride == RSASchemeNull {
			schemeHashAlg = public.Params.RSADetail().Scheme.Details.OAEP().HashAlg
		}
		if schemeHashAlg == HashAlgorithmNull {
			schemeHashAlg = public.NameAlg
		}
		if !schemeHashAlg.Supported() {
			return nil, fmt.Errorf("unknown scheme hash algorithm: %v", schemeHashAlg)
		}
		hash := schemeHashAlg.NewHash()
		labelCopy := make([]byte, len(label)+1)
		copy(labelCopy, label)
		return rsa.EncryptOAEP(hash, rand.Reader, pubKey, data, labelCopy)
	case RSASchemeRSAES:
		return rsa.EncryptPKCS1v15(rand.Reader, pubKey, data)
	}
	return nil, fmt.Errorf("unsupported RSA scheme: %v", padding)
}

func cryptGetECDHPoint(public *Public) (ECCParameter, *ECCPoint, error) {
	if public.Type != ObjectTypeECC {
		panic(fmt.Sprintf("Unsupported key type %v", public.Type))
	}

	curve := eccCurveToGoCurve(public.Params.ECCDetail().CurveID)
	if curve == nil {
		return nil, nil, fmt.Errorf("unsupported curve: %v", public.Params.ECCDetail().CurveID)
	}

	ephPriv, ephX, ephY, err := elliptic.GenerateKey(curve, rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot generate ephemeral ECC key: %v", err)
	}

	if !curve.IsOnCurve(ephX, ephY) {
		return nil, nil, fmt.Errorf("ephemeral public key is not on curve")
	}

	tpmX := new(big.Int).SetBytes(public.Unique.ECC().X)
	tpmY := new(big.Int).SetBytes(public.Unique.ECC().Y)

	mulX, _ := curve.ScalarMult(tpmX, tpmY, ephPriv)

	return ECCParameter(mulX.Bytes()), &ECCPoint{X: ECCParameter(ephX.Bytes()), Y: ECCParameter(ephY.Bytes())}, nil
}

func cryptComputeEncryptedSalt(public *Public) (EncryptedSecret, []byte, error) {
	if !public.NameAlg.Supported() {
		return nil, nil, fmt.Errorf("cannot determine size of unknown nameAlg %v", public.NameAlg)
	}
	digestSize := public.NameAlg.Size()

	switch public.Type {
	case ObjectTypeRSA:
		salt := make([]byte, digestSize)
		if _, err := rand.Read(salt); err != nil {
			return nil, nil, fmt.Errorf("cannot read random bytes for salt: %v", err)
		}
		encryptedSalt, err := cryptEncryptRSA(public, RSASchemeOAEP, salt, []byte("SECRET"))
		return encryptedSalt, salt, err
	case ObjectTypeECC:
		z, q, err := cryptGetECDHPoint(public)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to compute secret: %v", err)
		}
		encryptedSalt, err := MarshalToBytes(q)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to marshal ephemeral public key: %v", err)
		}
		salt := internal.KDFe(public.NameAlg.GetHash(), []byte(z), []byte("SECRET"), []byte(q.X), []byte(public.Unique.ECC().X), digestSize*8)
		return EncryptedSecret(encryptedSalt), salt, nil
	}

	return nil, nil, fmt.Errorf("unsupported key type %v", public.Type)
}
