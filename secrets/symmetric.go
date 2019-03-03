package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"io"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/kms"
)

type DecoderRing struct {
	kmsService  *kms.KMS
	masterKeyId string
}

func NewDecoderRing(masterKeyId string) (*DecoderRing, error) {
	session, err := session.NewSession()
	if err != nil {
		return nil, err
	}

	kmsService := kms.New(session)
	return &DecoderRing{kmsService: kmsService, masterKeyId: masterKeyId}, nil
}

func (ring DecoderRing) Encrypt(plaintext string) ([]byte, error) {
	dataKeyResult, err := ring.kmsService.GenerateDataKey(&kms.GenerateDataKeyInput{
		KeyId:   aws.String(ring.masterKeyId),
		KeySpec: aws.String("AES_128"),
	})
	if err != nil {
		return nil, err
	}
	keyPlaintext := dataKeyResult.Plaintext
	keyCiphertext := dataKeyResult.CiphertextBlob

	messagePlaintext := []byte(plaintext)
	block, err := aes.NewCipher(keyPlaintext)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	_, err = io.ReadFull(rand.Reader, nonce)
	if err != nil {
		return nil, err
	}

	messageCiphertext := gcm.Seal(nonce, nonce, messagePlaintext, nil)
	return append(keyCiphertext, messageCiphertext...), nil
}

func (ring DecoderRing) Decrypt(ciphertext []byte) (*string, error) {
	keyCiphertext := ciphertext[:16]
	decryptResult, err := ring.kmsService.Decrypt(&kms.DecryptInput{
		CiphertextBlob: keyCiphertext,
	})
	if err != nil {
		return nil, err
	}
	keyPlaintext := decryptResult.Plaintext

	block, err := aes.NewCipher(keyPlaintext)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := ciphertext[16 : 16+gcm.NonceSize()]
	messageCiphertext := ciphertext[16+gcm.NonceSize():]

	messagePlaintext, err := gcm.Open(nil, nonce, messageCiphertext, nil)
	if err != nil {
		return nil, err
	}
	return aws.String(string(messagePlaintext)), nil
}
