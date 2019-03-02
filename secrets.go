package main

import (
	"io/ioutil"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/kms"
	"github.com/aws/aws-sdk-go/service/s3"
)

func EnsureSecrets(bucketName string) error {
	session, err := session.NewSession()
	if err != nil {
		return err
	}

	s3Service := s3.New(session)
	response, err := s3Service.GetObject(&s3.GetObjectInput{Bucket: aws.String(bucketName), Key: aws.String("tls-certificates.tar.enc")})
	defer response.Body.Close()
	if err != nil {
		return err
	}
	ciphertext, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return err
	}

	//

	kmsService := kms.New(session)
	result, err := kmsService.Decrypt(&kms.DecryptInput{CiphertextBlob: ciphertext})
	if err != nil {
		return err
	}
	key := result.Plaintext

	return nil
}
