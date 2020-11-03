// Copyright 2020 The Swarm Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package chequebook

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethersphere/bee/pkg/logging"
	"github.com/ethersphere/bee/pkg/settlement/swap/transaction"
	"github.com/ethersphere/bee/pkg/storage"
	"github.com/ethersphere/sw3-bindings/v2/simpleswapfactory"
)

var (
	// ErrNoCashout is the error if there has not been any cashout action for the chequebook
	ErrNoCashout = errors.New("no prior cashout")
)

// CashoutService is the service responsible for managing cashout actions
type CashoutService interface {
	io.Closer
	// Start starts monitoring past transactions
	Start() error
	// SetNotifyBouncedFunc sets the notify function for bouncing chequebooks
	SetNotifyBouncedFunc(f NotifyBouncedFunc)
	// CashCheque sends a cashing transaction for the last cheque of the chequebook
	CashCheque(ctx context.Context, chequebook common.Address, recipient common.Address) (common.Hash, error)
	// CashoutStatus gets the status of the latest cashout transaction for the chequebook
	CashoutStatus(ctx context.Context, chequebookAddress common.Address) (*CashoutStatus, error)
}

type cashoutService struct {
	lock                  sync.Mutex
	logger                logging.Logger
	store                 storage.StateStorer
	simpleSwapBindingFunc SimpleSwapBindingFunc
	backend               transaction.Backend
	transactionService    transaction.Service
	chequebookABI         abi.ABI
	chequeStore           ChequeStore
	notifyBouncedFunc     NotifyBouncedFunc
	monitorCtx            context.Context
	monitorCtxCancel      context.CancelFunc
	wg                    sync.WaitGroup
}

// CashoutStatus is the action plus its result
type CashoutStatus struct {
	TxHash   common.Hash
	Cheque   SignedCheque // the cheque that was used to cashout which may be different from the latest cheque
	Result   *CashChequeResult
	Reverted bool
}

// CashChequeResult summarizes the result of a CashCheque or CashChequeBeneficiary call
type CashChequeResult struct {
	Beneficiary      common.Address // beneficiary of the cheque
	Recipient        common.Address // address which received the funds
	Caller           common.Address // caller of cashCheque
	TotalPayout      *big.Int       // total amount that was paid out in this call
	CumulativePayout *big.Int       // cumulative payout of the cheque that was cashed
	CallerPayout     *big.Int       // payout for the caller of cashCheque
	Bounced          bool           // indicates wether parts of the cheque bounced
}

// cashoutAction is the data we store for a cashout
type cashoutAction struct {
	TxHash   common.Hash
	Cheque   SignedCheque // the cheque that was used to cashout which may be different from the latest cheque
	Result   *CashChequeResult
	Reverted bool
}

// NotifyBouncedFunc is used to notify something about bounced chequebooks
type NotifyBouncedFunc = func(chequebook common.Address) error

// NewCashoutService creates a new CashoutService
func NewCashoutService(
	logger logging.Logger,
	store storage.StateStorer,
	simpleSwapBindingFunc SimpleSwapBindingFunc,
	backend transaction.Backend,
	transactionService transaction.Service,
	chequeStore ChequeStore,
) (CashoutService, error) {
	chequebookABI, err := abi.JSON(strings.NewReader(simpleswapfactory.ERC20SimpleSwapABI))
	if err != nil {
		return nil, err
	}

	monitorCtx, monitorCtxCancel := context.WithCancel(context.Background())

	return &cashoutService{
		logger:                logger,
		store:                 store,
		simpleSwapBindingFunc: simpleSwapBindingFunc,
		backend:               backend,
		transactionService:    transactionService,
		chequebookABI:         chequebookABI,
		chequeStore:           chequeStore,
		monitorCtx:            monitorCtx,
		monitorCtxCancel:      monitorCtxCancel,
	}, nil
}

func (s *cashoutService) SetNotifyBouncedFunc(f NotifyBouncedFunc) {
	s.notifyBouncedFunc = f
}

// cashoutActionKey computes the store key for the last cashout action for the chequebook
func cashoutActionKey(chequebook common.Address) string {
	return fmt.Sprintf("cashout_%x", chequebook)
}

// Start starts monitoring past transactions
func (s *cashoutService) Start() error {
	return s.store.Iterate("cashout_", func(key, value []byte) (stop bool, err error) {
		var cashoutAction cashoutAction
		err = s.store.Get(string(key), &cashoutAction)
		if err != nil {
			return false, err
		}

		if cashoutAction.Result == nil && !cashoutAction.Reverted {
			s.monitorCashChequeBeneficiaryTransaction(cashoutAction.Cheque.Chequebook, cashoutAction.TxHash)
		}

		return false, nil
	})
}

// CashCheque sends a cashout transaction for the last cheque of the chequebook
func (s *cashoutService) CashCheque(ctx context.Context, chequebook common.Address, recipient common.Address) (common.Hash, error) {
	s.lock.Lock()
	defer s.lock.Unlock()

	cheque, err := s.chequeStore.LastCheque(chequebook)
	if err != nil {
		return common.Hash{}, err
	}

	callData, err := s.chequebookABI.Pack("cashChequeBeneficiary", recipient, cheque.CumulativePayout, cheque.Signature)
	if err != nil {
		return common.Hash{}, err
	}

	request := &transaction.TxRequest{
		To:       chequebook,
		Data:     callData,
		GasPrice: nil,
		GasLimit: 0,
		Value:    big.NewInt(0),
	}

	txHash, err := s.transactionService.Send(ctx, request)
	if err != nil {
		return common.Hash{}, err
	}

	err = s.store.Put(cashoutActionKey(chequebook), &cashoutAction{
		TxHash:   txHash,
		Cheque:   *cheque,
		Result:   nil,
		Reverted: false,
	})
	if err != nil {
		return common.Hash{}, err
	}

	s.monitorCashChequeBeneficiaryTransaction(chequebook, txHash)

	return txHash, nil
}

func (s *cashoutService) monitorCashChequeBeneficiaryTransaction(chequebook common.Address, txHash common.Hash) {
	receiptC, errC := s.transactionService.WatchForReceipt(s.monitorCtx, txHash)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		select {
		case <-s.monitorCtx.Done():
			return
		case err := <-errC:
			if err == nil {
				return
			}
			s.logger.Errorf("failed to monitor transaction %x: %v", txHash, err)
		case receipt := <-receiptC:
			if receipt == nil {
				return
			}
			err := s.processCashChequeBeneficiaryReceipt(chequebook, receipt)
			if err != nil {
				s.logger.Errorf("could not process cashout receipt: %v", err)
			}
		}
	}()
}

func (s *cashoutService) processCashChequeBeneficiaryReceipt(chequebook common.Address, receipt *types.Receipt) error {
	s.lock.Lock()
	defer s.lock.Unlock()

	var action *cashoutAction
	err := s.store.Get(cashoutActionKey(chequebook), &action)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return ErrNoCashout
		}
		return err
	}

	// ignore if this is not the latest transaction
	if receipt.TxHash != action.TxHash {
		return nil
	}

	// this should never happen
	if receipt.Status == types.ReceiptStatusFailed {
		s.logger.Errorf("cashout transaction reverted: %x", action.TxHash)
		return s.store.Put(cashoutActionKey(chequebook), &cashoutAction{
			TxHash:   action.TxHash,
			Cheque:   action.Cheque,
			Result:   nil,
			Reverted: true,
		})
	}

	result, err := s.parseCashChequeBeneficiaryReceipt(chequebook, receipt)
	if err != nil {
		return fmt.Errorf("could not parse cashout receipt: %w", err)
	}

	err = s.store.Put(cashoutActionKey(chequebook), &cashoutAction{
		TxHash:   action.TxHash,
		Cheque:   action.Cheque,
		Result:   result,
		Reverted: false,
	})
	if err != nil {
		return err
	}

	if result.Bounced {
		s.logger.Infof("cashout bounced: %x", receipt.TxHash)
		err = s.notifyBouncedFunc(chequebook)
		if err != nil {
			return fmt.Errorf("notify bounced: %w", err)
		}
	} else {
		s.logger.Tracef("cashout confirmed: %x", receipt.TxHash)
	}

	return nil
}

// CashoutStatus gets the status of the latest cashout transaction for the chequebook
func (s *cashoutService) CashoutStatus(ctx context.Context, chequebookAddress common.Address) (*CashoutStatus, error) {
	var action *cashoutAction
	err := s.store.Get(cashoutActionKey(chequebookAddress), &action)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, ErrNoCashout
		}
		return nil, err
	}

	return &CashoutStatus{
		TxHash:   action.TxHash,
		Cheque:   action.Cheque,
		Result:   action.Result,
		Reverted: action.Reverted,
	}, nil
}

// parseCashChequeBeneficiaryReceipt processes the receipt from a CashChequeBeneficiary transaction
func (s *cashoutService) parseCashChequeBeneficiaryReceipt(chequebookAddress common.Address, receipt *types.Receipt) (*CashChequeResult, error) {
	result := &CashChequeResult{
		Bounced: false,
	}

	binding, err := s.simpleSwapBindingFunc(chequebookAddress, s.backend)
	if err != nil {
		return nil, err
	}

	for _, log := range receipt.Logs {
		if log.Address != chequebookAddress {
			continue
		}
		if event, err := binding.ParseChequeCashed(*log); err == nil {
			result.Beneficiary = event.Beneficiary
			result.Caller = event.Caller
			result.CallerPayout = event.CallerPayout
			result.TotalPayout = event.TotalPayout
			result.CumulativePayout = event.CumulativePayout
			result.Recipient = event.Recipient
		} else if _, err := binding.ParseChequeBounced(*log); err == nil {
			result.Bounced = true
		}
	}

	return result, nil
}

func (s *cashoutService) Close() error {
	s.monitorCtxCancel()
	s.wg.Wait()
	return nil
}

// Equal compares to CashChequeResults
func (r *CashChequeResult) Equal(o *CashChequeResult) bool {
	if r.Beneficiary != o.Beneficiary {
		return false
	}
	if r.Bounced != o.Bounced {
		return false
	}
	if r.Caller != o.Caller {
		return false
	}
	if r.CallerPayout.Cmp(o.CallerPayout) != 0 {
		return false
	}
	if r.CumulativePayout.Cmp(o.CumulativePayout) != 0 {
		return false
	}
	if r.Recipient != o.Recipient {
		return false
	}
	if r.TotalPayout.Cmp(o.TotalPayout) != 0 {
		return false
	}
	return true
}
