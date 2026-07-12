package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"io"
)

func Encrypt(plaintext []byte, key string) (string, error) {
	hash := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(hash[:])
	if err != nil {
		return "", err
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, aesGCM.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := aesGCM.Seal(nonce, nonce, plaintext, nil)
	return base64.RawStdEncoding.EncodeToString(ciphertext), nil
}

func Decrypt(encoded string, key string) ([]byte, error) {
	ciphertext, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}

	hash := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(hash[:])
	if err != nil {
		return nil, err
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := aesGCM.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, io.ErrUnexpectedEOF
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	return aesGCM.Open(nil, nonce, ciphertext, nil)
}

func TryDecrypt(encoded string, key string) []byte {
	b, err := Decrypt(encoded, key)
	if err != nil {
		return []byte(encoded)
	}
	return b
}
