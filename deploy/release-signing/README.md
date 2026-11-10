# Release signing — deploy/release-signing/

This directory holds the PUBLIC half of the Drevanta release signing key.

## What is signed

Every GitHub Release publishes `checksums.txt` (sha256 of each release
artifact) plus `checksums.txt.sig` — an Ed25519 signature over
`checksums.txt` made with the **private** release key. The public
one-command installer (`deploy/bootstrap.sh`) embeds the public key and
**refuses to install anything** when:

- `checksums.txt.sig` is missing,
- the signature does not verify, or
- the signing key does not match the embedded public key.

This means a compromised mirror cannot swap artifacts even together with
their checksums: the checksums file itself must be signed by the release
key whose private half never leaves GitHub Actions.

## Key facts

- Algorithm: Ed25519 (OpenSSL 1.1.1+ / 3.x, `openssl pkeyutl -rawin`).
- Public key (committed, embedded in bootstrap.sh):
  `drevanta-release.pub.pem`
- Private key: GitHub Actions **repository secret**
  `DREVANTA_RELEASE_SIGN_KEY` (unencrypted PEM). It exists nowhere else —
  not in the repository, not on workstations, not on the VPS.
- The release workflow verifies the secret key against the committed
  public half before signing, and **fails the release** if the secret is
  missing (we never publish unsigned artifacts).

## Rotation

1. Generate a new pair:
   `openssl genpkey -algorithm ed25519 -out new-sign.pem &&
    openssl pkey -in new-sign.pem -pubout > drevanta-release.pub.pem`
2. Update the public key in this directory AND the `EMBEDDED_PUBKEY`
   constant in `deploy/bootstrap.sh` (they must match byte for byte).
3. Store `new-sign.pem` as the `DREVANTA_RELEASE_SIGN_KEY` secret.
4. Delete the old private key from the secret store. Old releases keep
   verifying because each bootstrap pins the key of its own source tree.

## Verify a release by hand

```sh
curl -fsSLO https://github.com/backupamunga-ops/drevanta_server_manager/releases/download/vX.Y.Z/checksums.txt{,.sig}
openssl pkeyutl -verify -pubin \
  -inkey deploy/release-signing/drevanta-release.pub.pem \
  -rawin -in checksums.txt -sigfile checksums.txt.sig
sha256sum -c checksums.txt
```
