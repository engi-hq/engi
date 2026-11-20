// Package credential is the credential use case: it validates credentials
// against their type schema, encrypts them at rest, hands decrypted data to
// the engine at execution time and keeps OAuth2 tokens fresh.
//
// Plaintext lives only inside this package and in the CredentialData returned
// by Resolve. Nothing here logs credential data.
package credential
