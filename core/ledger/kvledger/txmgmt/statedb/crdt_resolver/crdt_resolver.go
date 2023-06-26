package crdt_resolver

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

func Resolve(curValue []byte, diffValue []byte, resType string) ([]byte, error) {

	switch resType {
	case "Set":
		return diffValue, nil
	case "IntAdd":
		return intAddResolve(curValue, diffValue)
	case "UintSub":
		return uintSubResolve(curValue, diffValue)
	case "StringConcat":
		return stringConcatResolve(curValue, diffValue)
	case "ArrayAppend":
		return arrayAppendResolve(curValue, diffValue)
	case "Wait": // Just for testing purpose. Useless otherwise.
		return waitResolve(diffValue)
	default:
		return []byte(""), fmt.Errorf("Unknown resolve type")
	}
}

func intAddResolve(curValue []byte, diffValue []byte) ([]byte, error) {
	var curNumber int
	var err error
	if len(curValue) != 0 {
		curNumber, err = strconv.Atoi(string(curValue))

		if err != nil {
			return []byte(""), err
		}
	}

	difNumber, err := strconv.Atoi(string(diffValue))

	if err != nil {
		return []byte(""), err
	}

	res, err := add(curNumber, difNumber)

	if err != nil {
		return []byte(""), err
	}

	return []byte(strconv.Itoa(res)), nil
}

func stringConcatResolve(curValue []byte, diffValue []byte) ([]byte, error) {
	return []byte(string(curValue) + string(diffValue)), nil
}

func arrayAppendResolve(curValue []byte, diffValue []byte) ([]byte, error) {
	var curArray []interface{}
	var diffArray []interface{}
	if len(curValue) != 0 {
		err := json.Unmarshal(curValue, &curArray)

		if err != nil {
			return []byte(""), err
		}
	}

	err := json.Unmarshal(diffValue, &diffArray)

	if err != nil {
		return []byte(""), err
	}

	res, err := json.Marshal(append(curArray, diffArray...))

	if err != nil {
		return []byte(""), err
	}

	return res, nil
}

func uintSubResolve(cur []byte, diff []byte) ([]byte, error) {
	curVal, err := strconv.Atoi(string(cur))
	if err != nil {
		return []byte(""), err
	}

	diffVal, err := strconv.Atoi(string(diff))
	if err != nil {
		return []byte(""), err
	}

	if diffVal < 0 {
		return []byte(""), fmt.Errorf("Can't have negative diff")
	}

	if curVal < diffVal {
		return []byte(""), fmt.Errorf("Negative result")
	}

	resValue, err := sub(curVal, diffVal)

	if err != nil {
		return []byte(""), err
	}

	return []byte(strconv.Itoa(resValue)), nil
}

func waitResolve(val []byte) ([]byte, error) {
	mils, err := strconv.Atoi(string(val))

	if err != nil {
		return []byte(""), err
	}

	time.Sleep(time.Duration(mils) * time.Millisecond)

	return []byte(""), nil
}

// sub two number checking for overflow
func sub(b int, q int) (int, error) {

	// Check overflow
	var diff int
	diff = b - q

	if (diff > b) == (b >= 0 && q >= 0) {
		return 0, fmt.Errorf("Math: Subtraction overflow occurred  %d - %d", b, q)
	}

	return diff, nil
}

func add(b int, q int) (int, error) {

	// Check overflow
	var sum int
	sum = q + b

	if (sum < q) == (b >= 0 && q >= 0) {
		return 0, fmt.Errorf("Math: addition overflow occurred %d + %d", b, q)
	}

	return sum, nil
}
