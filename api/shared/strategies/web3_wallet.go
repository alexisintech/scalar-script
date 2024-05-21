package strategies

import (
	"context"
	"errors"
	"fmt"

	"clerk/api/apierror"
	"clerk/api/shared/verifications"
	"clerk/model"
	"clerk/pkg/constants"
	"clerk/pkg/rand"
	"clerk/repository"
	"clerk/utils/database"
	"clerk/utils/param"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/jonboulle/clockwork"
)

type Web3WalletPreparer struct {
	clock      clockwork.Clock
	env        *model.Env
	web3Wallet *model.Identification

	identificationRepo *repository.Identification
	verificationRepo   *repository.Verification
}

func NewWeb3WalletPreparer(clock clockwork.Clock, env *model.Env, web3Wallet *model.Identification) Web3WalletPreparer {
	return Web3WalletPreparer{
		clock:              clock,
		env:                env,
		web3Wallet:         web3Wallet,
		identificationRepo: repository.NewIdentification(),
		verificationRepo:   repository.NewVerification(),
	}
}

func (p Web3WalletPreparer) Identification() *model.Identification {
	return p.web3Wallet
}

func (p Web3WalletPreparer) Prepare(ctx context.Context, tx database.Tx) (*model.Verification, error) {
	nonce, err := rand.Token()
	if err != nil {
		return nil, fmt.Errorf("Web3Wallet/prepare: creating verification nonce: %w", err)
	}

	verification, err := createVerification(ctx, tx, p.clock, &createVerificationParams{
		instanceID:       p.env.Instance.ID,
		strategy:         constants.VSWeb3MetamaskSignature,
		nonce:            &nonce,
		identificationID: &p.web3Wallet.ID,
	})
	if err != nil {
		return nil, fmt.Errorf("Web3Wallet/prepare: creating verification for public_address: %w", err)
	}

	return verification, nil
}

type Web3Attemptor struct {
	env *model.Env

	verificationService *verifications.Service
	verificationRepo    *repository.Verification

	identification *model.Identification
	verification   *model.Verification
	web3Signature  string
}

func NewWeb3Attemptor(
	env *model.Env, clock clockwork.Clock,
	identification *model.Identification,
	verification *model.Verification,
	web3Signature string,
) Web3Attemptor {
	return Web3Attemptor{
		env:                 env,
		verificationService: verifications.NewService(clock),
		verificationRepo:    repository.NewVerification(),
		identification:      identification,
		verification:        verification,
		web3Signature:       web3Signature,
	}
}

func (a Web3Attemptor) Attempt(ctx context.Context, tx database.Tx) (*model.Verification, error) {
	if err := checkVerificationStatus(ctx, tx, a.verificationService, a.verification); err != nil {
		return a.verification, err
	}

	isSignatureValid := verifySig(a.identification.Identifier.String, a.web3Signature, a.verification.Nonce.String)
	if err := logVerificationAttempt(ctx, tx, a.verificationRepo, a.verification, isSignatureValid); err != nil {
		return a.verification, err
	}

	if !isSignatureValid {
		return nil, ErrInvalidWeb3Signature
	}

	return a.verification, nil
}

func (Web3Attemptor) ToAPIError(err error) apierror.Error {
	if errors.Is(err, ErrInvalidWeb3Signature) {
		return apierror.FormIncorrectSignature(param.Web3Signature.Name)
	}

	return toAPIErrors(err)
}

// https://gist.github.com/dcb9/385631846097e1f59e3cba3b1d42f3ed#file-eth_sign_verify-go
func verifySig(web3Wallet, web3Signature, nonce string) bool {
	fromAddr := common.HexToAddress(web3Wallet)

	sig, err := hexutil.Decode(web3Signature)
	if err != nil {
		// safe to ignore the error, we just care that the signature is not valid
		return false
	}

	// EcRecover returns the address for the account that was used to create the signature.
	// Note, this function is compatible with eth_sign and personal_sign. As such it recovers
	// the address of:
	// hash = keccak256("\x19Ethereum Signed Message:\n"${message length}${message})
	// addr = ecrecover(hash, signature)
	//
	// Note, the signature must conform to the secp256k1 curve R, S and V values, where
	// the V value must be be 27 or 28 for legacy reasons.
	//
	// https://github.com/ethereum/go-ethereum/wiki/Management-APIs#personal_ecRecover
	//
	// Original code: https://github.com/ethereum/go-ethereum/blob/55599ee95d4151a2502465e0afc7c47bd1acba77/internal/ethapi/api.go#L442
	if sig[64] != 27 && sig[64] != 28 {
		return false
	}
	sig[64] -= 27

	pubKey, err := crypto.SigToPub(accounts.TextHash([]byte(nonce)), sig)
	if err != nil {
		return false
	}

	recoveredAddr := crypto.PubkeyToAddress(*pubKey)

	return fromAddr == recoveredAddr
}
