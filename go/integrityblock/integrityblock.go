package integrityblock

import (
	"bytes"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"

	"github.com/WICG/webpackage/go/internal/cbor"
)

type IntegritySignature struct {
	SignatureAttributes map[string][]byte
	Signature           []byte
}

type IntegrityBlock struct {
	Magic          []byte
	Version        []byte
	SignatureStack []*IntegritySignature
}

const (
	Ed25519publicKeyAttributeName = "ed25519PublicKey"
)

var IntegrityBlockMagic = []byte{0xf0, 0x9f, 0x96, 0x8b, 0xf0, 0x9f, 0x93, 0xa6}

// "b1" as bytes and 2 empty bytes
var VersionB1 = []byte{0x31, 0x62, 0x00, 0x00}

// CborBytes returns the CBOR encoded bytes of an integrity signature.
func (is *IntegritySignature) CborBytes() ([]byte, error) {
	var buf bytes.Buffer
	enc := cbor.NewEncoder(&buf)
	enc.EncodeArrayHeader(2)

	mes := []*cbor.MapEntryEncoder{}
	for key, value := range is.SignatureAttributes {
		mes = append(mes,
			cbor.GenerateMapEntry(func(keyE *cbor.Encoder, valueE *cbor.Encoder) {
				keyE.EncodeTextString(key)
				valueE.EncodeByteString(value)
			}))
	}
	if err := enc.EncodeMap(mes); err != nil {
		return nil, fmt.Errorf("integrityblock: Failed to encode signature attribute: %v", err)
	}

	if err := enc.EncodeByteString(is.Signature); err != nil {
		return nil, fmt.Errorf("integrityblock: Failed to encode signature: %v", err)
	}
	return buf.Bytes(), nil
}

// CborBytes returns the CBOR encoded bytes of the integrity block.
func (ib *IntegrityBlock) CborBytes() ([]byte, error) {
	var buf bytes.Buffer
	enc := cbor.NewEncoder(&buf)

	err := enc.EncodeArrayHeader(3)
	if err != nil {
		return nil, err
	}

	err = enc.EncodeByteString(ib.Magic)
	if err != nil {
		return nil, err
	}

	err = enc.EncodeByteString(ib.Version)
	if err != nil {
		return nil, err
	}

	err = enc.EncodeArrayHeader(len(ib.SignatureStack))
	for _, integritySignature := range ib.SignatureStack {
		isb, err := integritySignature.CborBytes()
		if err != nil {
			return nil, err
		}
		buf.Write(isb)
	}

	return buf.Bytes(), nil
}

// generateEmptyIntegrityBlock creates an empty integrity block which does not have any integrity signatures in the signature stack yet.
func generateEmptyIntegrityBlock() *IntegrityBlock {
	var integritySignatures []*IntegritySignature

	integrityBlock := &IntegrityBlock{
		Magic:          IntegrityBlockMagic,
		Version:        VersionB1,
		SignatureStack: integritySignatures,
	}
	return integrityBlock
}

// readWebBundlePayloadLength returns the length of the web bundle parsed from the last 8 bytes of the web bundle file.
// [Web Bundle's Trailing Length]: https://wicg.github.io/webpackage/draft-yasskin-wpack-bundled-exchanges.html#name-trailing-length
func readWebBundlePayloadLength(bundleFile *os.File) (int64, error) {
	// Finds the offset, from which the 8 bytes containing the web bundle length start.
	_, err := bundleFile.Seek(-8, io.SeekEnd)
	if err != nil {
		return 0, err
	}

	// Reads from the offset to the end of the file (those 8 bytes).
	webBundleLengthBytes, err := ioutil.ReadAll(bundleFile)
	if err != nil {
		return 0, err
	}

	return int64(binary.BigEndian.Uint64(webBundleLengthBytes)), nil
}

// obtainIntegrityBlock returns either the existing integrity block parsed (not supported in v1) or a newly
// created empty integrity block. Integrity block preceeds the actual web bundle bytes. The second return
// value marks the offset from which point onwards we need to copy the web bundle bytes from. It will be
// needed later in the signing process (TODO) because we cannot rely on the integrity block length, because
// we don't know if the integrity block already existed or not.
func ObtainIntegrityBlock(bundleFile *os.File) (*IntegrityBlock, int64, error) {
	webBundleLen, err := readWebBundlePayloadLength(bundleFile)
	if err != nil {
		return nil, 0, err
	}
	fileStats, err := bundleFile.Stat()
	if err != nil {
		return nil, 0, err
	}

	integrityBlockLen := fileStats.Size() - webBundleLen
	if integrityBlockLen < 0 {
		return nil, -1, errors.New("Integrity block length should never be negative. Web bundle length big endian seems to be bigger than the size of the file.")
	}

	if integrityBlockLen != 0 {
		// Read existing integrity block. Not supported in v1.
		return nil, integrityBlockLen, errors.New("Web bundle already contains an integrity block. Please provide an unsigned web bundle.")
	}

	integrityBlock := generateEmptyIntegrityBlock()
	return integrityBlock, integrityBlockLen, nil
}

// getLastSignatureAttributes returns the signature attributes from the newest (the first)
// signature stack or a new empty map if the signature stack is empty.
func GetLastSignatureAttributes(integrityBlock *IntegrityBlock) map[string][]byte {
	var signatureAttributes map[string][]byte
	if len(integrityBlock.SignatureStack) == 0 {
		signatureAttributes = make(map[string][]byte, 1)
	} else {
		signatureAttributes = (*integrityBlock.SignatureStack[0]).SignatureAttributes
	}
	return signatureAttributes
}

// ComputeWebBundleSha512 computes the SHA-512 hash over the given web bundle file.
func ComputeWebBundleSha512(bundleFile io.ReadSeeker, offset int64) ([]byte, error) {
	h := sha512.New()

	// Move the file pointer to the start of the web bundle bytes.
	bundleFile.Seek(offset, io.SeekStart)

	// io.Copy() will do chunked read/write under the hood
	_, err := io.Copy(h, bundleFile)
	if err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}
