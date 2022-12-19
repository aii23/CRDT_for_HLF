package blockdecoder

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"

	"github.com/golang/protobuf/proto"
	"github.com/hyperledger/fabric-protos-go/common"
	"github.com/hyperledger/fabric-protos-go/ledger/rwset"
	"github.com/hyperledger/fabric-protos-go/ledger/rwset/kvrwset"
	"github.com/hyperledger/fabric-protos-go/msp"
	"github.com/hyperledger/fabric-protos-go/peer"
)

func (b *Block) Display() {
	jsonData, err := json.MarshalIndent(b, "", "    ")
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(string(jsonData))
}

func (b *Block) DisplaySymplyfied() {
	simplyfiedBlock := b.makeSimplyfiedBlock()

	jsonData, err := json.MarshalIndent(simplyfiedBlock, "", "    ")
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println(string(jsonData))
}

func (b *Block) makeSimplyfiedBlock() SimplyfiedBlock {
	res := SimplyfiedBlock{
		SimplyfiedBlockData: make([]SimplyfiedBlockData, len(b.BlockData)),
	}

	for i, v := range b.BlockData {
		res.SimplyfiedBlockData[i] = makeSimplyfiedBlockData(v)
	}

	return res
}

func makeSimplyfiedBlockData(blockData BlockData) SimplyfiedBlockData {
	res := SimplyfiedBlockData{
		SimplyfiedTransactions: make([]SimplyfiedTransaction, len(blockData.Envelope.Data.Transactions)),
	}

	for i, v := range blockData.Envelope.Data.Transactions {
		res.SimplyfiedTransactions[i] = makeSimplyfiedTransaction(v)
	}

	return res
}

func makeSimplyfiedTransaction(v Transaction) SimplyfiedTransaction {
	res := SimplyfiedTransaction{
		RWSets: make([]RWSet, len(v.ChaincodeEndorsedAction.ProposalResponsePayload.ChaincodeKVRWSets)),
	}

	for i, v := range v.ChaincodeEndorsedAction.ProposalResponsePayload.ChaincodeKVRWSets {
		res.RWSets[i] = RWSet{
			Reads:  v.Reads,
			Writes: v.Writes,
		}
	}

	return res
}

func UnmarshalBlock(data []byte) Block {
	var err error
	block := &common.Block{}

	if err = proto.Unmarshal(data, block); err != nil {
		fmt.Println(err)
		return Block{}
	}

	blockHeader := unmarshalBlockHeader(block.Header)
	blockData := unmarshalBlockData(block.Data)
	blockMetadata := unmarshalBlockMetadata(block.Metadata)

	return Block{
		BlockHeader:   blockHeader,
		BlockData:     blockData,
		BlockMetadata: blockMetadata,
	}
}

func unmarshalBlockHeader(header *common.BlockHeader) BlockHeader {
	previousHash := sha256.Sum256(header.PreviousHash)
	dataHash := sha256.Sum256(header.DataHash)

	return BlockHeader{
		Number:       header.Number,
		PreviousHash: hex.EncodeToString(previousHash[:]),
		DataHash:     hex.EncodeToString(dataHash[:]),
	}
}

func unmarshalBlockData(data *common.BlockData) []BlockData {
	result := make([]BlockData, len(data.Data))
	for i := range data.Data {
		result[i] = unmarshalSingleBlockData(data.Data[i])
	}
	return result
}

func unmarshalSingleBlockData(data []byte) BlockData {
	envelope := &common.Envelope{}

	if err := proto.Unmarshal(data, envelope); err != nil {
		fmt.Println(err)
		return BlockData{}
	}

	payload := &common.Payload{}

	if err := proto.Unmarshal(envelope.Payload, payload); err != nil {
		fmt.Println(err)
		return BlockData{}
	}

	return BlockData{
		Envelope: Envelope{
			Header: unmarshalPayloadHeader(payload.Header),
			Data:   unmarshalPayloadData(payload.Data),
		},
	}
}

func unmarshalPayloadHeader(header *common.Header) Header {
	channelHeader := &common.ChannelHeader{}
	if err := proto.Unmarshal(header.ChannelHeader, channelHeader); err != nil {
		fmt.Println(err)
		return Header{}
	}

	signatureHeader := &common.SignatureHeader{}
	if err := proto.Unmarshal(header.SignatureHeader, signatureHeader); err != nil {
		fmt.Println(err)
		return Header{}
	}

	return Header{
		Payload: Payload{
			ChannelHeader:   unmarshalChannelHeader(channelHeader),
			SignatureHeader: unmarshalSignatureHeader(signatureHeader),
		},
	}
}

func unmarshalChannelHeader(channelHeader *common.ChannelHeader) ChannelHeader {

	HeaderType := [7]string{"MESSAGE", "CONFIG", "CONFIG_UPDATE", "ENDORSER_TRANSACTION",
		"ORDERER_TRANSACTION", "DELIVER_SEEK_INFO", "CHAINCODE_PACKAGE"}

	return ChannelHeader{
		Type:      HeaderType[channelHeader.Type],
		Version:   channelHeader.Version,
		ChannelId: channelHeader.ChannelId,
		TxId:      channelHeader.TxId,
		Epoch:     channelHeader.Epoch,
		Extension: unmarshalChaincodeHeaderExtension(channelHeader.Extension),
	}
}

func unmarshalChaincodeHeaderExtension(b []byte) ChaincodeHeaderExtension {
	extension := &peer.ChaincodeHeaderExtension{}
	if err := proto.Unmarshal(b, extension); err != nil {
		fmt.Println(err)
		return ChaincodeHeaderExtension{}
	}

	return ChaincodeHeaderExtension{
		ChaincodeId: ChaincodeID{
			Path:    extension.ChaincodeId.Path,
			Name:    extension.ChaincodeId.Name,
			Version: extension.ChaincodeId.Version,
		},
	}
}

func unmarshalSignatureHeader(signatureHeader *common.SignatureHeader) SignatureHeader {
	creator := &msp.SerializedIdentity{}

	if err := proto.Unmarshal(signatureHeader.Creator, creator); err != nil {
		fmt.Println()
		return SignatureHeader{}
	}

	uEnc := base64.URLEncoding.EncodeToString([]byte(creator.IdBytes))

	certHash, err := base64.URLEncoding.DecodeString(uEnc)
	if err != nil {
		fmt.Println()
		return SignatureHeader{}
	}

	end, _ := pem.Decode([]byte(string(certHash)))
	if end == nil {
		fmt.Println()
		return SignatureHeader{}
	}
	cert, err := x509.ParseCertificate(end.Bytes)
	if err != nil {
		fmt.Println()
		return SignatureHeader{}
	}

	certificate := Certificate{
		Country:            cert.Issuer.Country,
		Organization:       cert.Issuer.Organization,
		OrganizationalUnit: cert.Issuer.OrganizationalUnit,
		Locality:           cert.Issuer.Locality,
		Province:           cert.Issuer.Province,
		SerialNumber:       cert.Issuer.SerialNumber,
		NotBefore:          cert.NotBefore,
		NotAfter:           cert.NotAfter,
	}

	creatorJson := Creator{
		Mspid:       creator.Mspid,
		CertHash:    string(certHash),
		Certificate: certificate,
	}

	return SignatureHeader{
		Creator: creatorJson,
	}
}

func unmarshalPayloadData(b []byte) Data {
	transaction := &peer.Transaction{}

	if err := proto.Unmarshal(b, transaction); err != nil {
		fmt.Println(err)
		return Data{}
	}

	result := Data{
		Transactions: make([]Transaction, len(transaction.Actions)),
	}

	for i := range transaction.Actions {
		result.Transactions[i] = unmarshalActionPayload(transaction.Actions[i].Payload)
	}

	return result
}

func unmarshalActionPayload(b []byte) Transaction {
	chaincodeActionPayload := &peer.ChaincodeActionPayload{}

	if err := proto.Unmarshal(b, chaincodeActionPayload); err != nil {
		fmt.Println(err)
		return Transaction{}
	}

	return Transaction{
		ChaincodeProposalPayload: unmarshalChaincodeProposalPayload(chaincodeActionPayload.ChaincodeProposalPayload),
		ChaincodeEndorsedAction:  unmarshalProposalResponsePayload(chaincodeActionPayload.Action.ProposalResponsePayload),
	}
}

func unmarshalChaincodeProposalPayload(b []byte) ChaincodeProposalPayload {
	chaincodeProposalPayload := &peer.ChaincodeProposalPayload{}
	if err := proto.Unmarshal(b, chaincodeProposalPayload); err != nil {
		fmt.Println(err)
		return ChaincodeProposalPayload{}
	}

	input := &peer.ChaincodeInvocationSpec{}
	if err := proto.Unmarshal(chaincodeProposalPayload.Input, input); err != nil {
		fmt.Println(err)
		return ChaincodeProposalPayload{}
	}

	chaincodeArgs := make([]string, len(input.ChaincodeSpec.Input.Args))

	for i, c := range input.ChaincodeSpec.Input.Args {

		args := CToGoString(c[:])
		chaincodeArgs[i] = args
	}

	ChaincodeType := [5]string{"UNDEFINED", "GOLANG", "NODE", "CAR", "JAVA"}

	return ChaincodeProposalPayload{
		ChaincodeInvocationSpec: ChaincodeInvocationSpec{
			ChaincodeSpec: ChaincodeSpec{
				ChaincodeId:   input.ChaincodeSpec.ChaincodeId.Name,
				ChaincodeType: ChaincodeType[input.ChaincodeSpec.Type],
				ChaincodeArgs: chaincodeArgs,
			},
		},
	}
}

func unmarshalProposalResponsePayload(b []byte) ChaincodeEndorsedAction {
	proposalResponsePayload := &peer.ProposalResponsePayload{}
	if err := proto.Unmarshal(b, proposalResponsePayload); err != nil {
		fmt.Println(err)
		return ChaincodeEndorsedAction{}
	}

	proposalHash := sha256.Sum256(proposalResponsePayload.ProposalHash)

	chaincodeAction := &peer.ChaincodeAction{}
	if err := proto.Unmarshal(proposalResponsePayload.Extension, chaincodeAction); err != nil {
		fmt.Println(err)
		return ChaincodeEndorsedAction{}
	}

	chaincodeKVRWSets := unmarshalKVRWSets(chaincodeAction.Results)

	chaincodeEvent := &peer.ChaincodeEvent{}
	if err := proto.Unmarshal(chaincodeAction.Events, chaincodeEvent); err != nil {
		fmt.Println(err)
		return ChaincodeEndorsedAction{}
	}

	eventPayload := CToGoString(chaincodeEvent.Payload[:])

	chaincodeEventJson := ChaincodeEvents{
		ChaincodeId: chaincodeEvent.ChaincodeId,
		TxId:        chaincodeEvent.TxId,
		EventName:   chaincodeEvent.EventName,
		Payload:     eventPayload,
	}

	return ChaincodeEndorsedAction{
		ProposalResponsePayload: ProposalResponsePayload{
			ProposalHash:      hex.EncodeToString(proposalHash[:]),
			ChaincodeKVRWSets: chaincodeKVRWSets,
			ChaincodeEvents:   chaincodeEventJson,
		},
	}
}

func unmarshalKVRWSets(b []byte) []ChaincodeKVRWSet {
	txReadWriteSet := &rwset.TxReadWriteSet{}

	if err := proto.Unmarshal(b, txReadWriteSet); err != nil {
		fmt.Println(err)
		return make([]ChaincodeKVRWSet, 0)
	}

	result := make([]ChaincodeKVRWSet, len(txReadWriteSet.NsRwset))

	for i, v := range txReadWriteSet.NsRwset {
		result[i] = unmarshalRWSet(v.Rwset)
	}

	return result
}

func unmarshalRWSet(b []byte) ChaincodeKVRWSet {
	kvrwset := &kvrwset.KVRWSet{}

	if err := proto.Unmarshal(b, kvrwset); err != nil {
		fmt.Println()
		return ChaincodeKVRWSet{}
	}

	kvReads := make([]KVRead, len(kvrwset.Reads))
	kvWrites := make([]KVWrite, len(kvrwset.Writes))
	rangeQueryInfos := make([]RangeQueryInfo, len(kvrwset.RangeQueriesInfo))
	kvMetadataWrite := make([]KVMetadataWrite, len(kvrwset.MetadataWrites))
	crdtPayload := make([]CRDTPayload, len(kvrwset.CrdtPayload))

	for i, v := range kvrwset.Reads {
		kvReads[i] = KVRead{
			Key: v.Key,
			Version: Version{
				BlockNum: v.Version.BlockNum,
				TxNum:    v.Version.TxNum,
			},
		}
	}

	for i, v := range kvrwset.Writes {
		kvWrites[i] = KVWrite{
			Key:      v.Key,
			Value:    v.Value,
			IsDelete: v.IsDelete,
		}
	}

	for i, v := range kvrwset.RangeQueriesInfo {
		rangeQueryInfos[i] = RangeQueryInfo{
			StartKey:     v.StartKey,
			EndKey:       v.EndKey,
			ItrExhausted: v.ItrExhausted,
		}
	}

	for i, v := range kvrwset.MetadataWrites {
		kvMetadataWrite[i] = KVMetadataWrite{
			Key:  v.Key,
			Name: v.Entries[0].Name,
		}
	}

	for i, v := range kvrwset.CrdtPayload {
		crdtPayload[i] = CRDTPayload{
			data: v.Data,
		}
	}

	return ChaincodeKVRWSet{
		Reads:            kvReads,
		RangeQueriesInfo: rangeQueryInfos,
		Writes:           kvWrites,
		MetadataWrites:   kvMetadataWrite,
		CRDTPayloads:     crdtPayload,
	}
}

func unmarshalBlockMetadata(metadata *common.BlockMetadata) BlockMetadata {
	return BlockMetadata{}
	panic("unimplemented")
}

func CToGoString(c []byte) string {
	n := -1
	for i, b := range c {
		if b == 0 {
			break
		}
		n = i
	}
	return string(c[:n+1])
}
