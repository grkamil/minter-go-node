package transaction

import (
	"encoding/hex"
	"fmt"
	"github.com/MinterTeam/minter-go-node/core/code"
	"github.com/MinterTeam/minter-go-node/core/commissions"
	"github.com/MinterTeam/minter-go-node/core/state"
	"github.com/MinterTeam/minter-go-node/core/state/swap"
	"github.com/MinterTeam/minter-go-node/core/types"
	"github.com/tendermint/tendermint/libs/kv"
	"math/big"
)

type RemoveSwapPoolData struct {
	Coin0          types.CoinID
	Coin1          types.CoinID
	Liquidity      *big.Int
	MinimumVolume0 *big.Int
	MinimumVolume1 *big.Int
}

func (data RemoveSwapPoolData) basicCheck(tx *Transaction, context *state.CheckState) *Response {
	if data.Coin0 == data.Coin1 {
		return &Response{
			Code: code.CrossConvert,
			Log:  "\"From\" coin equals to \"to\" coin",
			Info: EncodeError(code.NewCrossConvert(
				data.Coin0.String(),
				data.Coin1.String(), "", "")),
		}
	}

	if !context.Swap().SwapPoolExist(data.Coin0, data.Coin1) {
		return &Response{
			Code: code.PairNotExists,
			Log:  "swap pool for pair not found",
		}
	}

	sender, _ := tx.Sender()
	if err := context.Swap().CheckBurn(sender, data.Coin0, data.Coin1, data.Liquidity, data.MinimumVolume0, data.MinimumVolume1); err != nil {
		wantAmount0, wantAmount1 := context.Swap().AmountsOfLiquidity(data.Coin0, data.Coin1, data.Liquidity)
		if err == swap.ErrorInsufficientLiquidityBalance {
			balance, amount0, amount1 := context.Swap().SwapPoolFromProvider(sender, data.Coin0, data.Coin1)
			if balance == nil {
				balance, amount0, amount1 = big.NewInt(0), big.NewInt(0), big.NewInt(0)
			}
			return &Response{
				Code: code.InsufficientLiquidityBalance,
				Log:  fmt.Sprintf("Insufficient balance for provider: %s liquidity tokens is equal %s of coin %d and %s of coin %d, but you want to get %s, %s of coin %d and %s of coin %d", balance, amount0, data.Coin0, amount1, data.Coin1, data.Liquidity, wantAmount0, data.Coin0, wantAmount1, data.Coin1),
				Info: EncodeError(code.NewInsufficientLiquidityBalance(balance.String(), amount0.String(), data.Coin0.String(), amount1.String(), data.Coin1.String(), data.Liquidity.String(), wantAmount0.String(), wantAmount1.String())),
			}
		}
		if err == swap.ErrorInsufficientLiquidityBurned {
			wantGetAmount0 := data.MinimumVolume0.String()
			wantGetAmount1 := data.MinimumVolume1.String()
			return &Response{
				Code: code.InsufficientLiquidityBurned,
				Log:  fmt.Sprintf("You wanted to get more %s of coin %d and more %s of coin %d, but currently liquidity %s is equal %s of coin %d and  %s of coin %d", wantGetAmount0, data.Coin0, wantGetAmount1, data.Coin1, data.Liquidity, wantAmount0, data.Coin0, wantAmount1, data.Coin1),
				Info: EncodeError(code.NewInsufficientLiquidityBurned(wantGetAmount0, data.Coin0.String(), wantGetAmount1, data.Coin1.String(), data.Liquidity.String(), wantAmount0.String(), wantAmount1.String())),
			}
		}
		return &Response{
			Code: code.SwapPoolUnknown,
			Log:  err.Error(),
		}
	}
	return nil
}

func (data RemoveSwapPoolData) String() string {
	return fmt.Sprintf("REMOVE SWAP POOL")
}

func (data RemoveSwapPoolData) Gas() int64 {
	return commissions.RemoveSwapPoolData
}

func (data RemoveSwapPoolData) Run(tx *Transaction, context state.Interface, rewardPool *big.Int, currentBlock uint64) Response {
	sender, _ := tx.Sender()

	var checkState *state.CheckState
	var isCheck bool
	if checkState, isCheck = context.(*state.CheckState); !isCheck {
		checkState = state.NewCheckState(context.(*state.State))
	}

	response := data.basicCheck(tx, checkState)
	if response != nil {
		return *response
	}

	commissionInBaseCoin := tx.CommissionInBaseCoin()
	gasCoin := checkState.Coins().GetCoin(tx.GasCoin)
	commission, isGasCommissionFromPoolSwap, errResp := CalculateCommission(checkState, gasCoin, commissionInBaseCoin)
	if errResp != nil {
		return *errResp
	}

	if checkState.Accounts().GetBalance(sender, tx.GasCoin).Cmp(commission) < 0 {
		return Response{
			Code: code.InsufficientFunds,
			Log:  fmt.Sprintf("Insufficient funds for sender account: %s. Wanted %s %s", sender.String(), commission.String(), gasCoin.GetFullSymbol()),
			Info: EncodeError(code.NewInsufficientFunds(sender.String(), commission.String(), gasCoin.GetFullSymbol(), gasCoin.ID().String())),
		}
	}

	if deliverState, ok := context.(*state.State); ok {
		amount0, amount1 := deliverState.Swap.PairBurn(sender, data.Coin0, data.Coin1, data.Liquidity, data.MinimumVolume0, data.MinimumVolume1)

		if isGasCommissionFromPoolSwap {
			commission, commissionInBaseCoin = deliverState.Swap.PairSell(tx.GasCoin, types.GetBaseCoinID(), commission, commissionInBaseCoin)
		} else {
			deliverState.Coins.SubVolume(tx.GasCoin, commission)
			deliverState.Coins.SubReserve(tx.GasCoin, commissionInBaseCoin)
		}
		deliverState.Accounts.SubBalance(sender, tx.GasCoin, commission)
		rewardPool.Add(rewardPool, commissionInBaseCoin)

		deliverState.Accounts.AddBalance(sender, data.Coin0, amount0)
		deliverState.Accounts.AddBalance(sender, data.Coin1, amount1)

		deliverState.Accounts.SetNonce(sender, tx.Nonce)
	}

	tags := kv.Pairs{
		kv.Pair{Key: []byte("tx.type"), Value: []byte(hex.EncodeToString([]byte{byte(TypeRemoveSwapPool)}))},
		kv.Pair{Key: []byte("tx.from"), Value: []byte(hex.EncodeToString(sender[:]))},
	}

	return Response{
		Code:      code.OK,
		GasUsed:   tx.Gas(),
		GasWanted: tx.Gas(),
		Tags:      tags,
	}
}
