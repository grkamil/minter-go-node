package transaction

import (
	"encoding/hex"
	"fmt"
	"github.com/MinterTeam/minter-go-node/core/code"
	"github.com/MinterTeam/minter-go-node/core/commissions"
	"github.com/MinterTeam/minter-go-node/core/state"
	"github.com/MinterTeam/minter-go-node/core/types"
	"github.com/MinterTeam/minter-go-node/formula"
	"github.com/tendermint/tendermint/libs/kv"
	"log"
	"math/big"
)

type SellAllCoinData struct {
	CoinToSell        types.CoinID
	CoinToBuy         types.CoinID
	MinimumValueToBuy *big.Int
}

func (data SellAllCoinData) totalSpend(tx *Transaction, context *state.CheckState) (totalSpends, []conversion, *big.Int, *Response) {
	sender, _ := tx.Sender()

	total := totalSpends{}
	var conversions []conversion

	commissionInBaseCoin := tx.CommissionInBaseCoin() // todo CalculateCommission
	available := context.Accounts().GetBalance(sender, data.CoinToSell)
	var value *big.Int

	total.Add(data.CoinToSell, available)

	switch {
	case data.CoinToSell.IsBaseCoin():
		amountToSell := big.NewInt(0).Sub(available, commissionInBaseCoin)

		if amountToSell.Sign() != 1 {
			return nil, nil, nil, &Response{
				Code: code.InsufficientFunds,
				Log:  "Insufficient funds for sender account",
				Info: EncodeError(code.NewInsufficientFunds(sender.String(), commissionInBaseCoin.String(), types.GetBaseCoin().String(), types.GetBaseCoinID().String())),
			}
		}

		coin := context.Coins().GetCoin(data.CoinToBuy)
		value = formula.CalculatePurchaseReturn(coin.Volume(), coin.Reserve(), coin.Crr(), amountToSell)

		if value.Cmp(data.MinimumValueToBuy) == -1 {
			return nil, nil, nil, &Response{
				Code: code.MinimumValueToBuyReached,
				Log:  fmt.Sprintf("You wanted to get minimum %s, but currently you will get %s", data.MinimumValueToBuy.String(), value.String()),
				Info: EncodeError(code.NewMinimumValueToBuyReached(data.MinimumValueToBuy.String(), value.String(), coin.GetFullSymbol(), coin.ID().String())),
			}
		}

		if errResp := CheckForCoinSupplyOverflow(coin, value); errResp != nil {
			return nil, nil, nil, errResp
		}

		conversions = append(conversions, conversion{
			FromCoin:  data.CoinToSell,
			ToCoin:    data.CoinToBuy,
			ToAmount:  value,
			ToReserve: amountToSell,
		})
	case data.CoinToBuy.IsBaseCoin():
		amountToSell := big.NewInt(0).Set(available)

		coin := context.Coins().GetCoin(data.CoinToSell)
		ret := formula.CalculateSaleReturn(coin.Volume(), coin.Reserve(), coin.Crr(), amountToSell)

		if ret.Cmp(data.MinimumValueToBuy) == -1 {
			return nil, nil, nil, &Response{
				Code: code.MinimumValueToBuyReached,
				Log:  fmt.Sprintf("You wanted to get minimum %s, but currently you will get %s", data.MinimumValueToBuy.String(), ret.String()),
				Info: EncodeError(code.NewMinimumValueToBuyReached(data.MinimumValueToBuy.String(), ret.String(), coin.GetFullSymbol(), coin.ID().String())),
			}
		}

		if ret.Cmp(commissionInBaseCoin) == -1 {
			return nil, nil, nil, &Response{
				Code: code.InsufficientFunds,
				Log:  "Insufficient funds for sender account",
				Info: EncodeError(code.NewInsufficientFunds(sender.String(), commissionInBaseCoin.String(), coin.GetFullSymbol(), coin.ID().String())),
			}
		}

		value = big.NewInt(0).Set(ret)
		value.Sub(ret, commissionInBaseCoin)

		conversions = append(conversions, conversion{
			FromCoin:    data.CoinToSell,
			FromAmount:  amountToSell,
			FromReserve: ret,
			ToCoin:      data.CoinToBuy,
		})
	default:
		amountToSell := big.NewInt(0).Set(available)

		coinFrom := context.Coins().GetCoin(data.CoinToSell)
		coinTo := context.Coins().GetCoin(data.CoinToBuy)

		basecoinValue := formula.CalculateSaleReturn(coinFrom.Volume(), coinFrom.Reserve(), coinFrom.Crr(), amountToSell)
		log.Println(commissionInBaseCoin)
		log.Println(basecoinValue)
		log.Println(tx.GasCoin)
		log.Println(data.CoinToSell)
		if basecoinValue.Cmp(commissionInBaseCoin) == -1 {
			return nil, nil, nil, &Response{
				Code: code.InsufficientFunds,
				Log:  "Insufficient funds for sender account",
				Info: EncodeError(code.NewInsufficientFunds(sender.String(), commissionInBaseCoin.String(), coinFrom.GetFullSymbol(), coinFrom.ID().String())),
			}
		}

		basecoinValue.Sub(basecoinValue, commissionInBaseCoin)

		value = formula.CalculatePurchaseReturn(coinTo.Volume(), coinTo.Reserve(), coinTo.Crr(), basecoinValue)
		if value.Cmp(data.MinimumValueToBuy) == -1 {
			return nil, nil, nil, &Response{
				Code: code.MinimumValueToBuyReached,
				Log:  fmt.Sprintf("You wanted to get minimum %s, but currently you will get %s", data.MinimumValueToBuy.String(), value.String()),
				Info: EncodeError(code.NewMinimumValueToBuyReached(data.MinimumValueToBuy.String(), value.String(), coinTo.GetFullSymbol(), coinTo.ID().String())),
			}
		}

		if errResp := CheckForCoinSupplyOverflow(coinTo, value); errResp != nil {
			return nil, nil, nil, errResp
		}

		conversions = append(conversions, conversion{
			FromCoin:    data.CoinToSell,
			FromAmount:  amountToSell,
			FromReserve: big.NewInt(0).Add(basecoinValue, commissionInBaseCoin),
			ToCoin:      data.CoinToBuy,
			ToAmount:    value,
			ToReserve:   basecoinValue,
		})
	}

	return total, conversions, value, nil
}

func (data SellAllCoinData) basicCheck(tx *Transaction, context *state.CheckState) *Response {
	coinToSell := context.Coins().GetCoin(data.CoinToSell)
	if coinToSell == nil {
		return &Response{
			Code: code.CoinNotExists,
			Log:  "Coin to sell not exists",
			Info: EncodeError(code.NewCoinNotExists("", data.CoinToSell.String())),
		}
	}

	if !coinToSell.BaseOrHasReserve() {
		return &Response{
			Code: code.CoinHasNotReserve,
			Log:  "sell coin has not reserve",
			Info: EncodeError(code.NewCoinHasNotReserve(
				coinToSell.GetFullSymbol(),
				coinToSell.ID().String(),
			)),
		}
	}

	coinToBuy := context.Coins().GetCoin(data.CoinToBuy)
	if coinToBuy == nil {
		return &Response{
			Code: code.CoinNotExists,
			Log:  "Coin to buy not exists",
			Info: EncodeError(code.NewCoinNotExists("", data.CoinToBuy.String())),
		}
	}

	if !coinToBuy.BaseOrHasReserve() {
		return &Response{
			Code: code.CoinHasNotReserve,
			Log:  "buy coin has not reserve",
			Info: EncodeError(code.NewCoinHasNotReserve(
				coinToBuy.GetFullSymbol(),
				coinToBuy.ID().String(),
			)),
		}
	}

	if data.CoinToSell == data.CoinToBuy {
		return &Response{
			Code: code.CrossConvert,
			Log:  "\"From\" coin equals to \"to\" coin",
			Info: EncodeError(code.NewCrossConvert(
				data.CoinToSell.String(),
				coinToSell.GetFullSymbol(),
				data.CoinToBuy.String(),
				coinToBuy.GetFullSymbol()),
			),
		}
	}

	return nil
}

func (data SellAllCoinData) String() string {
	return fmt.Sprintf("SELL ALL COIN sell:%s buy:%s",
		data.CoinToSell.String(), data.CoinToBuy.String())
}

func (data SellAllCoinData) Gas() int64 {
	return commissions.ConvertTx
}

func (data SellAllCoinData) Run(tx *Transaction, context state.Interface, rewardPool *big.Int, currentBlock uint64) Response {
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

	// _, _, _, response = data.totalSpend(tx, checkState)
	// if response != nil {
	// 	return *response
	// }

	commissionInBaseCoin := tx.CommissionInBaseCoin()
	commissionPoolSwapper := checkState.Swap().GetSwapper(tx.GasCoin, types.GetBaseCoinID())
	// gasCoin := checkState.Coins().GetCoin(tx.GasCoin)
	gasCoin := checkState.Coins().GetCoin(data.CoinToSell)
	commission, isGasCommissionFromPoolSwap, errResp := CalculateCommission(checkState, commissionPoolSwapper, gasCoin, commissionInBaseCoin)
	if errResp != nil {
		return *errResp
	}

	coinToSell := data.CoinToSell
	coinToBuy := data.CoinToBuy
	var coinFrom calculateCoin
	coinFrom = checkState.Coins().GetCoin(coinToSell)
	coinTo := checkState.Coins().GetCoin(coinToBuy)

	if checkState.Accounts().GetBalance(sender, tx.GasCoin).Cmp(commission) == -1 {
		return Response{
			Code: code.InsufficientFunds,
			Log:  fmt.Sprintf("Insufficient funds for sender account: %s. Wanted %s %s", sender.String(), commission.String(), gasCoin.GetFullSymbol()),
			Info: EncodeError(code.NewInsufficientFunds(sender.String(), commission.String(), gasCoin.GetFullSymbol(), gasCoin.ID().String())),
		}
	}
	balance := checkState.Accounts().GetBalance(sender, data.CoinToSell)
	valueToSell := big.NewInt(0).Set(balance)
	if tx.GasCoin == data.CoinToSell {
		valueToSell.Sub(valueToSell, commission)
	}

	// saleReturn := formula.CalculateSaleReturn(coinFrom.Volume(), coinFrom.Reserve(), coinFrom.Crr(), balance)
	// if saleReturn.Cmp(commissionInBaseCoin) == -1 {
	// 	return Response{
	// 		Code: code.InsufficientFunds,
	// 		Log:  "Insufficient funds for sender account",
	// 		Info: EncodeError(code.NewInsufficientFunds(sender.String(), commissionInBaseCoin.String(), coinFrom.GetFullSymbol(), coinFrom.ID().String())),
	// 	}
	// } // todo

	value := big.NewInt(0).Set(valueToSell)
	if value.Sign() != 1 {
		symbol := checkState.Coins().GetCoin(data.CoinToSell).GetFullSymbol()
		return Response{
			Code: code.InsufficientFunds,
			Log:  fmt.Sprintf("Insufficient funds for sender account: %s. Wanted %s %s", sender.String(), balance.String(), symbol),
			Info: EncodeError(code.NewInsufficientFunds(sender.String(), balance.String(), symbol, data.CoinToSell.String())),
		}
	}

	gasCoinEdited := dummyCoin{
		id:         gasCoin.ID(),
		volume:     gasCoin.Volume(),
		reserve:    gasCoin.Reserve(),
		crr:        gasCoin.Crr(),
		fullSymbol: gasCoin.GetFullSymbol(),
	}

	if !isGasCommissionFromPoolSwap && coinToSell == gasCoin.ID() {
		gasCoinEdited.volume.Sub(gasCoinEdited.volume, commission)
		gasCoinEdited.reserve.Sub(gasCoinEdited.reserve, commissionInBaseCoin)
		coinFrom = gasCoinEdited
	}

	if !coinToSell.IsBaseCoin() {
		value, errResp = CalculateSaleReturnAndCheck(coinFrom, value)
		if errResp != nil {
			return *errResp
		}
	}
	diffBipReserve := big.NewInt(0).Set(value)
	if !coinToBuy.IsBaseCoin() {
		value = formula.CalculatePurchaseReturn(coinTo.Volume(), coinTo.Reserve(), coinTo.Crr(), value)
		if errResp := CheckForCoinSupplyOverflow(coinTo, value); errResp != nil {
			return *errResp
		}
	}

	spendInGasCoin := big.NewInt(0).Set(commission)
	if tx.GasCoin != coinToSell {
		if value.Cmp(data.MinimumValueToBuy) == -1 {
			return Response{
				Code: code.MinimumValueToBuyReached,
				Log: fmt.Sprintf(
					"You wanted to buy minimum %s, but currently you need to spend %s to complete tx",
					data.MinimumValueToBuy.String(), value.String()),
				Info: EncodeError(code.NewMaximumValueToSellReached(data.MinimumValueToBuy.String(), value.String(), coinFrom.GetFullSymbol(), coinFrom.ID().String())),
			}
		}
		if checkState.Accounts().GetBalance(sender, data.CoinToSell).Cmp(value) < 0 {
			return Response{
				Code: code.InsufficientFunds,
				Log:  fmt.Sprintf("Insufficient funds for sender account: %s. Wanted %s %s", sender.String(), value.String(), coinFrom.GetFullSymbol()),
				Info: EncodeError(code.NewInsufficientFunds(sender.String(), value.String(), coinFrom.GetFullSymbol(), coinFrom.ID().String())),
			}
		}
	} else {
		spendInGasCoin.Add(spendInGasCoin, valueToSell)
	}
	if spendInGasCoin.Cmp(data.MinimumValueToBuy) == -1 {
		return Response{
			Code: code.MinimumValueToBuyReached,
			Log: fmt.Sprintf(
				"You wanted to buy minimum %s, but currently you need to spend %s to complete tx",
				data.MinimumValueToBuy.String(), spendInGasCoin.String()),
			Info: EncodeError(code.NewMaximumValueToSellReached(data.MinimumValueToBuy.String(), spendInGasCoin.String(), coinFrom.GetFullSymbol(), coinFrom.ID().String())),
		}
	}
	if checkState.Accounts().GetBalance(sender, tx.GasCoin).Cmp(spendInGasCoin) < 0 {
		return Response{
			Code: code.InsufficientFunds,
			Log:  fmt.Sprintf("Insufficient funds for sender account: %s. Wanted %s %s", sender.String(), spendInGasCoin.String(), gasCoin.GetFullSymbol()),
			Info: EncodeError(code.NewInsufficientFunds(sender.String(), spendInGasCoin.String(), gasCoin.GetFullSymbol(), gasCoin.ID().String())),
		}
	}

	if deliverState, ok := context.(*state.State); ok {
		if isGasCommissionFromPoolSwap {
			commission, commissionInBaseCoin = deliverState.Swap.PairSell(tx.GasCoin, types.GetBaseCoinID(), commission, commissionInBaseCoin)
		} else if !tx.GasCoin.IsBaseCoin() {
			deliverState.Coins.SubVolume(tx.GasCoin, commission)
			deliverState.Coins.SubReserve(tx.GasCoin, commissionInBaseCoin)
		}
		deliverState.Accounts.SubBalance(sender, tx.GasCoin, commission)
		rewardPool.Add(rewardPool, commissionInBaseCoin)
		deliverState.Accounts.SubBalance(sender, data.CoinToSell, valueToSell)
		if !data.CoinToSell.IsBaseCoin() {
			deliverState.Coins.SubVolume(data.CoinToSell, valueToSell)
			deliverState.Coins.SubReserve(data.CoinToSell, diffBipReserve)
		}
		deliverState.Accounts.AddBalance(sender, data.CoinToBuy, value)
		if !data.CoinToBuy.IsBaseCoin() {
			deliverState.Coins.AddVolume(data.CoinToBuy, value)
			deliverState.Coins.AddReserve(data.CoinToBuy, diffBipReserve)
		}
		deliverState.Accounts.SetNonce(sender, tx.Nonce)
	}

	tags := kv.Pairs{
		kv.Pair{Key: []byte("tx.commission_amount"), Value: []byte(commission.String())},
		kv.Pair{Key: []byte("tx.type"), Value: []byte(hex.EncodeToString([]byte{byte(TypeSellAllCoin)}))},
		kv.Pair{Key: []byte("tx.from"), Value: []byte(hex.EncodeToString(sender[:]))},
		kv.Pair{Key: []byte("tx.coin_to_buy"), Value: []byte(data.CoinToBuy.String())},
		kv.Pair{Key: []byte("tx.coin_to_sell"), Value: []byte(data.CoinToSell.String())},
		kv.Pair{Key: []byte("tx.return"), Value: []byte(value.String())},
		kv.Pair{Key: []byte("tx.sell_amount"), Value: []byte(balance.String())},
	}

	return Response{
		Code:      code.OK,
		Tags:      tags,
		GasUsed:   tx.Gas(),
		GasWanted: tx.Gas(),
	}
}
