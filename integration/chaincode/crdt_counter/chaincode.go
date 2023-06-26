/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package crdt_counter

import (
	"fmt"

	"github.com/hyperledger/fabric-chaincode-go/shim"
	pb "github.com/hyperledger/fabric-protos-go/peer"
)

type CRDT_counter struct{}

func (t *CRDT_counter) Init(stub shim.ChaincodeStubInterface) pb.Response {
	// Nothing to do in Init
	fmt.Println("Init invoked")
	fmt.Println("Init returning with success")
	return shim.Success(nil)
}

func (t *CRDT_counter) Invoke(stub shim.ChaincodeStubInterface) pb.Response {
	function, args := stub.GetFunctionAndParameters()
	switch function {
	case "invoke":
		// Increase counter
		return t.invoke(stub, args)
	case "query":
		return t.query(stub, args)
	default:
		return shim.Error(`Invalid invoke function name. Expecting invoke`)
	}
}

// Transaction makes payment of X units from A to B
func (t *CRDT_counter) invoke(stub shim.ChaincodeStubInterface, args []string) pb.Response {
	// var err error

	if len(args) != 3 {
		return shim.Error("Incorrect number of arguments. Expecting 3")
	}

	// _, err = strconv.Atoi(args[2])
	// if err != nil {
	// 	return shim.Error("Expecting integer value for asset holding")
	// }

	stub.PutCRDT(args[0], args[1], []byte(args[2]))

	return shim.Success(nil)
}

func (t *CRDT_counter) query(stub shim.ChaincodeStubInterface, args []string) pb.Response {
	var A string // Entities
	var err error

	if len(args) != 1 {
		return shim.Error("Incorrect number of arguments. Expecting name of the person to query")
	}

	A = args[0]

	// Get the state from the ledger
	Avalbytes, err := stub.GetCRDTState(A)
	if err != nil {
		jsonResp := "{\"Error\":\"Failed to get state for " + A + "\"}"
		return shim.Error(jsonResp)
	}

	if Avalbytes == nil {
		jsonResp := "{\"Error\":\"Nil amount for " + A + "\"}"
		return shim.Error(jsonResp)
	}

	jsonResp := "{\"Name\":\"" + A + "\",\"Amount\":\"" + string(Avalbytes) + "\"}"
	fmt.Printf("Query Response:%s\n", jsonResp)
	return shim.Success(Avalbytes)
}
