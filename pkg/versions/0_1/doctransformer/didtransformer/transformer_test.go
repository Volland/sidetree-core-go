/*
Copyright SecureKey Technologies Inc. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package didtransformer

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"testing"

	"github.com/btcsuite/btcutil/base58"
	"github.com/stretchr/testify/require"

	"github.com/trustbloc/sidetree-core-go/pkg/api/protocol"
	"github.com/trustbloc/sidetree-core-go/pkg/document"
	"github.com/trustbloc/sidetree-core-go/pkg/util/pubkey"
)

const testID = "doc:abc:123"

func TestNewTransformer(t *testing.T) {
	transformer := New()
	require.NotNil(t, transformer)
	require.Empty(t, transformer.methodCtx)
	require.Equal(t, false, transformer.includeBase)

	const ctx1 = "ctx-1"
	transformer = New(WithMethodContext([]string{ctx1}))
	require.Equal(t, 1, len(transformer.methodCtx))
	require.Equal(t, ctx1, transformer.methodCtx[0])

	const ctx2 = "ctx-2"
	transformer = New(WithMethodContext([]string{ctx1, ctx2}))
	require.Equal(t, 2, len(transformer.methodCtx))
	require.Equal(t, ctx2, transformer.methodCtx[1])

	transformer = New(WithBase(true))
	require.Equal(t, true, transformer.includeBase)
}

func TestTransformDocument(t *testing.T) {
	r := reader(t, "testdata/doc.json")
	docBytes, err := ioutil.ReadAll(r)
	require.NoError(t, err)
	doc, err := document.FromBytes(docBytes)
	require.NoError(t, err)

	transformer := New()

	internal := &protocol.ResolutionModel{Doc: doc, RecoveryCommitment: "recovery", UpdateCommitment: "update"}

	t.Run("success", func(t *testing.T) {
		info := make(protocol.TransformationInfo)
		info[document.IDProperty] = testID
		info[document.PublishedProperty] = true

		result, err := transformer.TransformDocument(internal, info)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Equal(t, testID, result.Document[document.IDProperty])
		require.Equal(t, true, result.MethodMetadata[document.PublishedProperty])
		require.Equal(t, "recovery", result.MethodMetadata[document.RecoveryCommitmentProperty])
		require.Equal(t, "update", result.MethodMetadata[document.UpdateCommitmentProperty])
		require.Empty(t, result.DocumentMetadata)

		jsonTransformed, err := json.Marshal(result.Document)
		require.NoError(t, err)

		didDoc, err := document.DidDocumentFromBytes(jsonTransformed)
		require.NoError(t, err)
		require.Equal(t, 1, len(didDoc.Context()))
		require.Equal(t, didContext, didDoc.Context()[0])

		// validate services
		service := didDoc.Services()[0]
		require.Equal(t, service.ID(), testID+"#hub")
		require.Equal(t, "https://example.com/hub/", service.ServiceEndpoint().(string))
		require.Equal(t, "recipientKeysValue", service["recipientKeys"])
		require.Equal(t, "routingKeysValue", service["routingKeys"])
		require.Equal(t, "IdentityHub", service.Type())

		service = didDoc.Services()[1]
		require.Equal(t, service.ID(), testID+"#hub-object")
		require.NotEmpty(t, service.ServiceEndpoint())
		require.Empty(t, service["recipientKeys"])
		require.Equal(t, "IdentityHub", service.Type())

		serviceEndpointEntry := service.ServiceEndpoint()
		serviceEndpoint := serviceEndpointEntry.(map[string]interface{})
		require.Equal(t, "https://schema.identity.foundation/hub", serviceEndpoint["@context"])
		require.Equal(t, "UserHubEndpoint", serviceEndpoint["type"])
		require.Equal(t, []interface{}{"did:example:456", "did:example:789"}, serviceEndpoint["instances"])

		// validate public keys
		pk := didDoc.VerificationMethods()[0]
		require.Contains(t, pk.ID(), testID)
		require.NotEmpty(t, pk.Type())
		require.NotEmpty(t, pk.PublicKeyJwk())
		require.Empty(t, pk.PublicKeyBase58())

		expectedPublicKeys := []string{"master", "general", "authentication", "assertion", "agreement", "delegation", "invocation"}
		require.Equal(t, len(expectedPublicKeys), len(didDoc.VerificationMethods()))

		expectedAuthenticationKeys := []string{"master", "authentication"}
		require.Equal(t, len(expectedAuthenticationKeys), len(didDoc.Authentications()))

		expectedAssertionMethodKeys := []string{"master", "assertion"}
		require.Equal(t, len(expectedAssertionMethodKeys), len(didDoc.AssertionMethods()))

		expectedAgreementKeys := []string{"master", "agreement"}
		require.Equal(t, len(expectedAgreementKeys), len(didDoc.AgreementKeys()))

		expectedDelegationKeys := []string{"master", "delegation"}
		require.Equal(t, len(expectedDelegationKeys), len(didDoc.DelegationKeys()))

		expectedInvocationKeys := []string{"master", "invocation"}
		require.Equal(t, len(expectedInvocationKeys), len(didDoc.InvocationKeys()))
	})

	t.Run("success - with canonical ID", func(t *testing.T) {
		info := make(protocol.TransformationInfo)
		info[document.IDProperty] = "did:abc:123"
		info[document.PublishedProperty] = true
		info[document.CanonicalIDProperty] = "canonical"

		result, err := transformer.TransformDocument(internal, info)
		require.NoError(t, err)
		require.Equal(t, "did:abc:123", result.Document[document.IDProperty])
		require.Equal(t, true, result.MethodMetadata[document.PublishedProperty])
		require.Equal(t, "recovery", result.MethodMetadata[document.RecoveryCommitmentProperty])
		require.Equal(t, "update", result.MethodMetadata[document.UpdateCommitmentProperty])
		require.Equal(t, "canonical", result.DocumentMetadata[document.CanonicalIDProperty])
	})

	t.Run("error - internal document is missing", func(t *testing.T) {
		info := make(protocol.TransformationInfo)
		info[document.IDProperty] = testID
		info[document.PublishedProperty] = true

		result, err := transformer.TransformDocument(nil, info)
		require.Error(t, err)
		require.Nil(t, result)
		require.Contains(t, err.Error(), "resolution model is required for document transformation")
	})

	t.Run("error - transformation info is missing", func(t *testing.T) {
		result, err := transformer.TransformDocument(internal, nil)
		require.Error(t, err)
		require.Nil(t, result)
		require.Contains(t, err.Error(), "transformation info is required for document transformation")
	})

	t.Run("error - transformation info is missing id", func(t *testing.T) {
		info := make(protocol.TransformationInfo)
		info[document.PublishedProperty] = true

		result, err := transformer.TransformDocument(internal, info)
		require.Error(t, err)
		require.Nil(t, result)
		require.Contains(t, err.Error(), "id is required for document transformation")
	})

	t.Run("error - transformation info is missing published", func(t *testing.T) {
		info := make(protocol.TransformationInfo)
		info[document.IDProperty] = testID

		result, err := transformer.TransformDocument(internal, info)
		require.Error(t, err)
		require.Nil(t, result)
		require.Contains(t, err.Error(), "published is required for document transformation")
	})
}

func TestWithMethodContext(t *testing.T) {
	doc := make(document.Document)

	transformer := New(WithMethodContext([]string{"ctx-1", "ctx-2"}))

	internal := &protocol.ResolutionModel{Doc: doc}

	info := make(protocol.TransformationInfo)
	info[document.IDProperty] = testID
	info[document.PublishedProperty] = true

	result, err := transformer.TransformDocument(internal, info)
	require.NoError(t, err)

	jsonTransformed, err := json.Marshal(result.Document)
	require.NoError(t, err)

	didDoc, err := document.DidDocumentFromBytes(jsonTransformed)
	require.NoError(t, err)
	require.Equal(t, 3, len(didDoc.Context()))
	require.Equal(t, "ctx-1", didDoc.Context()[1])
	require.Equal(t, "ctx-2", didDoc.Context()[2])
}

func TestWithBase(t *testing.T) {
	r := reader(t, "testdata/doc.json")
	docBytes, err := ioutil.ReadAll(r)
	require.NoError(t, err)
	doc, err := document.FromBytes(docBytes)
	require.NoError(t, err)

	transformer := New(WithBase(true))

	internal := &protocol.ResolutionModel{Doc: doc}

	info := make(protocol.TransformationInfo)
	info[document.IDProperty] = testID
	info[document.PublishedProperty] = true

	result, err := transformer.TransformDocument(internal, info)
	require.NoError(t, err)

	jsonTransformed, err := json.Marshal(result.Document)
	require.NoError(t, err)

	didDoc, err := document.DidDocumentFromBytes(jsonTransformed)
	require.NoError(t, err)
	require.Equal(t, 2, len(didDoc.Context()))

	// second context is @base
	baseMap := didDoc.Context()[1].(map[string]interface{})
	baseMap["@base"] = testID

	// validate service id doesn't contain document id
	service := didDoc.Services()[0]
	require.NotContains(t, service.ID(), testID)

	// validate public key id doesn't contain document id
	pk := didDoc.VerificationMethods()[0]
	require.NotContains(t, pk.ID(), testID)
}

func TestEd25519VerificationKey2018(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	jwk, err := pubkey.GetPublicKeyJWK(publicKey)
	require.NoError(t, err)

	publicKeyBytes, err := json.Marshal(jwk)
	require.NoError(t, err)

	data := fmt.Sprintf(ed25519DocTemplate, string(publicKeyBytes))
	doc, err := document.FromBytes([]byte(data))
	require.NoError(t, err)

	transformer := New()

	internal := &protocol.ResolutionModel{Doc: doc}

	info := make(protocol.TransformationInfo)
	info[document.IDProperty] = testID
	info[document.PublishedProperty] = true

	result, err := transformer.TransformDocument(internal, info)
	require.NoError(t, err)

	jsonTransformed, err := json.Marshal(result.Document)
	require.NoError(t, err)

	didDoc, err := document.DidDocumentFromBytes(jsonTransformed)
	require.NoError(t, err)
	require.Equal(t, didDoc.VerificationMethods()[0].Controller(), didDoc.ID())
	require.Equal(t, didContext, didDoc.Context()[0])

	// validate service
	service := didDoc.Services()[0]
	require.Contains(t, service.ID(), testID)
	require.NotEmpty(t, service.ServiceEndpoint())
	require.Equal(t, "OpenIdConnectVersion1.0Service", service.Type())

	// validate public key
	pk := didDoc.VerificationMethods()[0]
	require.Contains(t, pk.ID(), testID)
	require.Equal(t, "Ed25519VerificationKey2018", pk.Type())
	require.Empty(t, pk.PublicKeyJwk())

	// test base58 encoding
	require.Equal(t, base58.Encode(publicKey), pk.PublicKeyBase58())

	// validate length of expected keys
	expectedPublicKeys := []string{"assertion"}
	require.Equal(t, len(expectedPublicKeys), len(didDoc.VerificationMethods()))

	expectedAssertionMethodKeys := []string{"assertion"}
	require.Equal(t, len(expectedAssertionMethodKeys), len(didDoc.AssertionMethods()))

	require.Equal(t, 0, len(didDoc.Authentications()))
	require.Equal(t, 0, len(didDoc.AgreementKeys()))
}

func TestEd25519VerificationKey2018_Error(t *testing.T) {
	doc, err := document.FromBytes([]byte(ed25519Invalid))
	require.NoError(t, err)

	transformer := New()

	internal := &protocol.ResolutionModel{Doc: doc}

	info := make(protocol.TransformationInfo)
	info[document.IDProperty] = testID
	info[document.PublishedProperty] = true

	result, err := transformer.TransformDocument(internal, info)
	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, err.Error(), "unknown curve")
}

func reader(t *testing.T, filename string) io.Reader {
	f, err := os.Open(filename)
	require.NoError(t, err)

	return f
}

const ed25519DocTemplate = `{
  "publicKey": [
	{
  		"id": "assertion",
  		"type": "Ed25519VerificationKey2018",
		"purposes": ["assertionMethod"],
  		"publicKeyJwk": %s
	}
  ],
  "service": [
	{
	   "id": "oidc",
	   "type": "OpenIdConnectVersion1.0Service",
	   "serviceEndpoint": "https://openid.example.com/"
	}
  ]
}`

const ed25519Invalid = `{
  "publicKey": [
	{
  		"id": "assertion",
  		"type": "Ed25519VerificationKey2018",
		"purposes": ["assertionMethod"],
      	"publicKeyJwk": {
        	"kty": "OKP",
        	"crv": "curve",
        	"x": "PUymIqdtF_qxaAqPABSw-C-owT1KYYQbsMKFM-L9fJA",
        	"y": "nM84jDHCMOTGTh_ZdHq4dBBdo4Z5PkEOW9jA8z8IsGc"
      	}
	}
  ]
}`
