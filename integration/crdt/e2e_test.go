/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package crdt

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	bd "github.com/hyperledger/fabric/integration/crdt/blockdecoder"

	"github.com/golang/protobuf/proto"
	pb "github.com/hyperledger/fabric-protos-go/peer"

	//	"github.com/hyperledger/fabric/protoutil"
	"github.com/hyperledger/fabric-protos-go/common"
	"github.com/hyperledger/fabric-protos-go/ledger/rwset"
	"github.com/hyperledger/fabric-protos-go/ledger/rwset/kvrwset"

	"github.com/hyperledger/fabric-lib-go/healthz"
	"github.com/hyperledger/fabric/integration/channelparticipation"
	"github.com/hyperledger/fabric/integration/nwo"
	"github.com/hyperledger/fabric/integration/nwo/commands"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/gexec"
	"github.com/tedsuo/ifrit"
)

var _ = Describe("EndToEnd", func() {
	var (
		testDir string
		//		client    *docker.Client
		network              *nwo.Network
		chaincode            nwo.Chaincode
		crdt_chaincode       nwo.Chaincode
		erc20_crdt_chaincode nwo.Chaincode
		process              ifrit.Process
	)

	BeforeEach(func() {
		var err error
		testDir, err = ioutil.TempDir("", "crdt")
		Expect(err).NotTo(HaveOccurred())

		//		client, err = docker.NewClientFromEnv()
		Expect(err).NotTo(HaveOccurred())

		chaincode = nwo.Chaincode{
			Name:            "mycc",
			Version:         "0.0",
			Path:            components.Build("github.com/hyperledger/fabric/integration/chaincode/crdt/cmd"),
			Lang:            "binary",
			PackageFile:     filepath.Join(testDir, "simplecc.tar.gz"),
			Ctor:            `{"Args":["init","a","100"]}`,
			SignaturePolicy: `AND ('Org1MSP.member','Org2MSP.member')`,
			Sequence:        "1",
			InitRequired:    true,
			Label:           "my_prebuilt_chaincode",
		}

		crdt_chaincode = nwo.Chaincode{
			Name:            "mycc",
			Version:         "0.0",
			Path:            components.Build("github.com/hyperledger/fabric/integration/chaincode/crdt_counter/cmd"),
			Lang:            "binary",
			PackageFile:     filepath.Join(testDir, "simplecc.tar.gz"),
			Ctor:            `{"Args":["init"]}`,
			SignaturePolicy: `AND ('Org1MSP.member','Org2MSP.member')`,
			Sequence:        "1",
			InitRequired:    true,
			Label:           "my_prebuilt_chaincode",
		}

		erc20_crdt_chaincode = nwo.Chaincode{
			Name:            "mycc",
			Version:         "0.0",
			Path:            components.Build("github.com/hyperledger/fabric/integration/chaincode/crdt_erc20/cmd"),
			Lang:            "binary",
			PackageFile:     filepath.Join(testDir, "simplecc.tar.gz"),
			Ctor:            `{"Args":["Initialize", "Token", "T", "6"]}`, // ?
			SignaturePolicy: `AND ('Org1MSP.member','Org2MSP.member')`,
			Sequence:        "1",
			InitRequired:    true,
			Label:           "my_prebuilt_chaincode",
		}
	})

	AfterEach(func() {
		if process != nil {
			process.Signal(syscall.SIGTERM)
			Eventually(process.Wait(), network.EventuallyTimeout).Should(Receive())
		}
		if network != nil {
			network.Cleanup()
		}
		os.RemoveAll(testDir)
	})

	Describe("basic etcdraft network with 2 orgs and no docker", func() {
		var (
			metricsReader        *MetricsReader
			runArtifactsFilePath string
		)

		BeforeEach(func() {
			metricsReader = NewMetricsReader()
			go metricsReader.Start()

			network = nwo.New(nwo.BasicEtcdRaft(), testDir, nil, StartPort(), components)
			network.MetricsProvider = "statsd"
			network.StatsdEndpoint = metricsReader.Address()
			network.Consensus.ChannelParticipationEnabled = true
			network.Profiles = append(network.Profiles, &nwo.Profile{
				Name:          "TwoOrgsBaseProfileChannel",
				Consortium:    "SampleConsortium",
				Orderers:      []string{"orderer"},
				Organizations: []string{"Org1", "Org2"},
			})
			network.Channels = append(network.Channels, &nwo.Channel{
				Name:        "baseprofilechannel",
				Profile:     "TwoOrgsBaseProfileChannel",
				BaseProfile: "SampleDevModeEtcdRaft",
			})

			runArtifactsFilePath = filepath.Join(testDir, "run-artifacts.txt")
			os.Setenv("RUN_ARTIFACTS_FILE", runArtifactsFilePath)
			for i, e := range network.ExternalBuilders {
				e.PropagateEnvironment = append(e.PropagateEnvironment, "RUN_ARTIFACTS_FILE")
				network.ExternalBuilders[i] = e
			}

			network.GenerateConfigTree()
			for _, peer := range network.PeersWithChannel("testchannel") {
				core := network.ReadPeerConfig(peer)
				core.VM = nil
				network.WritePeerConfig(peer, core)
			}

			network.Bootstrap()

			networkRunner := network.NetworkGroupRunner()
			process = ifrit.Invoke(networkRunner)
			Eventually(process.Ready(), network.EventuallyTimeout).Should(BeClosed())
		})

		AfterEach(func() {
			if metricsReader != nil {
				metricsReader.Close()
			}

			// Terminate the processes but defer the network cleanup to the outer
			// AfterEach.
			if process != nil {
				process.Signal(syscall.SIGTERM)
				Eventually(process.Wait(), network.EventuallyTimeout).Should(Receive())
				process = nil
			}

			// Ensure that the temporary directories generated by launched external
			// chaincodes have been cleaned up. This must be done after the peers
			// have been terminated.
			contents, err := ioutil.ReadFile(runArtifactsFilePath)
			Expect(err).NotTo(HaveOccurred())
			scanner := bufio.NewScanner(bytes.NewBuffer(contents))
			for scanner.Scan() {
				Expect(scanner.Text()).NotTo(BeAnExistingFile())
			}
		})

		It("executes a basic etcdraft network with 2 orgs and no docker", func() {
			By("getting the orderer by name")
			orderer := network.Orderer("orderer")

			By("setting up the channel")
			network.CreateAndJoinChannel(orderer, "testchannel")
			nwo.EnableCapabilities(network, "testchannel", "Application", "V2_5", orderer, network.Peer("Org1", "peer0"), network.Peer("Org2", "peer0"))

			By("listing channels with osnadmin")
			tlsdir := network.OrdererLocalTLSDir(orderer)
			sess, err := network.Osnadmin(commands.ChannelList{
				OrdererAddress: network.OrdererAddress(orderer, nwo.AdminPort),
				CAFile:         filepath.Join(tlsdir, "ca.crt"),
				ClientCert:     filepath.Join(tlsdir, "server.crt"),
				ClientKey:      filepath.Join(tlsdir, "server.key"),
			})
			Expect(err).NotTo(HaveOccurred())
			Eventually(sess).Should(gexec.Exit(0))
			var channelList channelparticipation.ChannelList
			err = json.Unmarshal(sess.Out.Contents(), &channelList)
			Expect(err).NotTo(HaveOccurred())
			Expect(channelList).To(Equal(channelparticipation.ChannelList{
				SystemChannel: &channelparticipation.ChannelInfoShort{
					Name: "systemchannel",
					URL:  "/participation/v1/channels/systemchannel",
				},
				Channels: []channelparticipation.ChannelInfoShort{{
					Name: "testchannel",
					URL:  "/participation/v1/channels/testchannel",
				}},
			}))

			By("attempting to install unsupported chaincode without docker")
			badCC := chaincode
			badCC.Lang = "unsupported-type"
			badCC.Label = "chaincode-label"
			badCC.PackageFile = filepath.Join(testDir, "unsupported-type.tar.gz")
			nwo.PackageChaincodeBinary(badCC)
			badCC.SetPackageIDFromPackageFile()
			sess, err = network.PeerAdminSession(
				network.Peer("Org1", "peer0"),
				commands.ChaincodeInstall{
					PackageFile: badCC.PackageFile,
					ClientAuth:  network.ClientAuthRequired,
				},
			)
			Expect(err).NotTo(HaveOccurred())
			Eventually(sess, network.EventuallyTimeout).Should(gexec.Exit(1))
			Expect(sess.Err).To(gbytes.Say("docker build is disabled"))

			By("deploying the chaincode")
			nwo.DeployChaincode(network, "testchannel", orderer, chaincode)

			By("ensuring external cc run artifacts exist after deploying")
			contents, err := ioutil.ReadFile(runArtifactsFilePath)
			Expect(err).NotTo(HaveOccurred())
			scanner := bufio.NewScanner(bytes.NewBuffer(contents))
			for scanner.Scan() {
				Expect(scanner.Text()).To(BeADirectory())
			}

			By("getting the client peer by name")
			peer := network.Peer("Org1", "peer0")

			outputBlock := filepath.Join(testDir, "newest_block.pb")
			fetchNewest := commands.ChannelFetch{
				ChannelID:  "testchannel",
				Block:      "newest",
				OutputFile: outputBlock,
			}

			sess, err = network.PeerAdminSession(peer, fetchNewest)
			Expect(err).NotTo(HaveOccurred())
			Eventually(sess, network.EventuallyTimeout).Should(gexec.Exit(0))
			Expect(sess.Err).To(gbytes.Say("Received block: "))

			/// First Run
			RunInvoke(network, orderer, peer, "testchannel", "b", "25")

			sess, err = network.PeerAdminSession(peer, fetchNewest)
			Expect(err).NotTo(HaveOccurred())
			Eventually(sess, network.EventuallyTimeout).Should(gexec.Exit(0))
			Expect(sess.Err).To(gbytes.Say("Received block: "))

			showBlock(outputBlock)

			PrintQueryResponse(network, orderer, peer, "testchannel", "b")

			/// Second Run
			RunInvoke(network, orderer, peer, "testchannel", "b", "25")

			sess, err = network.PeerAdminSession(peer, fetchNewest)
			Expect(err).NotTo(HaveOccurred())
			Eventually(sess, network.EventuallyTimeout).Should(gexec.Exit(0))
			Expect(sess.Err).To(gbytes.Say("Received block: "))

			showBlock(outputBlock)

			PrintQueryResponse(network, orderer, peer, "testchannel", "b")
		})

		It("Crdt counter check", func() {
			By("getting the orderer by name")
			orderer := network.Orderer("orderer")

			By("setting up the channel")
			network.CreateAndJoinChannel(orderer, "testchannel")
			nwo.EnableCapabilities(network, "testchannel", "Application", "V2_5", orderer, network.Peer("Org1", "peer0"), network.Peer("Org2", "peer0"))

			By("listing channels with osnadmin")
			tlsdir := network.OrdererLocalTLSDir(orderer)
			sess, err := network.Osnadmin(commands.ChannelList{
				OrdererAddress: network.OrdererAddress(orderer, nwo.AdminPort),
				CAFile:         filepath.Join(tlsdir, "ca.crt"),
				ClientCert:     filepath.Join(tlsdir, "server.crt"),
				ClientKey:      filepath.Join(tlsdir, "server.key"),
			})
			Expect(err).NotTo(HaveOccurred())
			Eventually(sess).Should(gexec.Exit(0))
			var channelList channelparticipation.ChannelList
			err = json.Unmarshal(sess.Out.Contents(), &channelList)
			Expect(err).NotTo(HaveOccurred())
			Expect(channelList).To(Equal(channelparticipation.ChannelList{
				SystemChannel: &channelparticipation.ChannelInfoShort{
					Name: "systemchannel",
					URL:  "/participation/v1/channels/systemchannel",
				},
				Channels: []channelparticipation.ChannelInfoShort{{
					Name: "testchannel",
					URL:  "/participation/v1/channels/testchannel",
				}},
			}))

			By("deploying the chaincode")
			nwo.DeployChaincode(network, "testchannel", orderer, crdt_chaincode)

			By("ensuring external cc run artifacts exist after deploying")
			contents, err := ioutil.ReadFile(runArtifactsFilePath)
			Expect(err).NotTo(HaveOccurred())
			scanner := bufio.NewScanner(bytes.NewBuffer(contents))
			for scanner.Scan() {
				Expect(scanner.Text()).To(BeADirectory())
			}

			By("getting the client peer by name")
			peer := network.Peer("Org1", "peer0")

			outputBlock := filepath.Join(testDir, "newest_block.pb")
			fetchNewest := commands.ChannelFetch{
				ChannelID:  "testchannel",
				Block:      "newest",
				OutputFile: outputBlock,
			}

			sess, err = network.PeerAdminSession(peer, fetchNewest)
			Expect(err).NotTo(HaveOccurred())
			Eventually(sess, network.EventuallyTimeout).Should(gexec.Exit(0))
			Expect(sess.Err).To(gbytes.Say("Received block: "))

			RunInvoke2(network, orderer, peer, "testchannel", "IntAdd", "a", "33")
			RunInvoke2(network, orderer, peer, "testchannel", "IntAdd", "a", "55")

			RunInvoke2(network, orderer, peer, "testchannel", "StringConcat", "b", "Hello")
			RunInvoke2(network, orderer, peer, "testchannel", "StringConcat", "b", " ")
			RunInvoke2(network, orderer, peer, "testchannel", "StringConcat", "b", "world")
			RunInvoke2(network, orderer, peer, "testchannel", "StringConcat", "b", "!")

			encArr1, _ := json.Marshal([]int{1, 2, 3})
			encArr2, _ := json.Marshal([]int{4, 5, 6})
			encArr3, _ := json.Marshal([]int{7, 8, 9})

			RunInvoke2(network, orderer, peer, "testchannel", "ArrayAppend", "c", string(encArr1))
			RunInvoke2(network, orderer, peer, "testchannel", "ArrayAppend", "c", string(encArr2))
			RunInvoke2(network, orderer, peer, "testchannel", "ArrayAppend", "c", string(encArr3))

			sess, err = network.PeerAdminSession(peer, fetchNewest)
			Expect(err).NotTo(HaveOccurred())
			Eventually(sess, network.EventuallyTimeout).Should(gexec.Exit(0))
			Expect(sess.Err).To(gbytes.Say("Received block: "))

			showBlock(outputBlock)

			PrintQueryResponse(network, orderer, peer, "testchannel", "a")
			PrintQueryResponse(network, orderer, peer, "testchannel", "b")
			PrintQueryResponse(network, orderer, peer, "testchannel", "c")
		})

		It("Crdt Erc20 check", func() {
			By("getting the orderer by name")
			orderer := network.Orderer("orderer")

			By("setting up the channel")
			network.CreateAndJoinChannel(orderer, "testchannel")
			nwo.EnableCapabilities(network, "testchannel", "Application", "V2_5", orderer, network.Peer("Org1", "peer0"), network.Peer("Org2", "peer0"))

			By("listing channels with osnadmin")
			tlsdir := network.OrdererLocalTLSDir(orderer)
			sess, err := network.Osnadmin(commands.ChannelList{
				OrdererAddress: network.OrdererAddress(orderer, nwo.AdminPort),
				CAFile:         filepath.Join(tlsdir, "ca.crt"),
				ClientCert:     filepath.Join(tlsdir, "server.crt"),
				ClientKey:      filepath.Join(tlsdir, "server.key"),
			})
			Expect(err).NotTo(HaveOccurred())
			Eventually(sess).Should(gexec.Exit(0))
			var channelList channelparticipation.ChannelList
			err = json.Unmarshal(sess.Out.Contents(), &channelList)
			Expect(err).NotTo(HaveOccurred())
			Expect(channelList).To(Equal(channelparticipation.ChannelList{
				SystemChannel: &channelparticipation.ChannelInfoShort{
					Name: "systemchannel",
					URL:  "/participation/v1/channels/systemchannel",
				},
				Channels: []channelparticipation.ChannelInfoShort{{
					Name: "testchannel",
					URL:  "/participation/v1/channels/testchannel",
				}},
			}))

			By("deploying the chaincode")
			nwo.DeployChaincode(network, "testchannel", orderer, erc20_crdt_chaincode)

			By("ensuring external cc run artifacts exist after deploying")
			contents, err := ioutil.ReadFile(runArtifactsFilePath)
			Expect(err).NotTo(HaveOccurred())
			scanner := bufio.NewScanner(bytes.NewBuffer(contents))
			for scanner.Scan() {
				Expect(scanner.Text()).To(BeADirectory())
			}

			By("getting the client peer by name")
			peer := network.Peer("Org1", "peer0")
			// peer2 := network.Peer("Org2", "peer0")

			outputBlock := filepath.Join(testDir, "newest_block.pb")
			fetchNewest := commands.ChannelFetch{
				ChannelID:  "testchannel",
				Block:      "newest",
				OutputFile: outputBlock,
			}

			sess, err = network.PeerAdminSession(peer, fetchNewest)
			Expect(err).NotTo(HaveOccurred())
			Eventually(sess, network.EventuallyTimeout).Should(gexec.Exit(0))
			Expect(sess.Err).To(gbytes.Say("Received block: "))

			RunInvoke0(network, orderer, peer, "testchannel", "Mint", "10000")

			sess, err = network.PeerAdminSession(peer, fetchNewest)
			Expect(err).NotTo(HaveOccurred())
			Eventually(sess, network.EventuallyTimeout).Should(gexec.Exit(0))
			Expect(sess.Err).To(gbytes.Say("Received block: "))

			showBlock(outputBlock)

			// RunInvoke0(network, orderer, peer, "testchannel", "Transfer", peer2.ID(), "55000")

			//            showBlock(outputBlock)

			// PrintQueryResponse(network, orderer, peer, "testchannel", peer.ID())
		})
	})
})

func showBlock(outputBlock string) {
	// Block = header + data + metadata

	blockBytes, err := ioutil.ReadFile(outputBlock)
	if err != nil {
		fmt.Println(err)
		return
	}

	block := bd.UnmarshalBlock(blockBytes)
	block.Display()
	block.DisplaySymplyfied()
}

func showBlockHeader(header *common.BlockHeader) {
	fmt.Printf("Header: {\n")
	fmt.Printf("\tNumber: %d\n", header.Number)
	fmt.Printf("\tPreviousHash: %x\n", header.PreviousHash)
	fmt.Printf("\tDataHash: %x\n", header.DataHash)
	fmt.Printf("}\n")
}

func showBlockMetadata(metadata *common.BlockMetadata) {
	fmt.Println("Metadata")
}

func showBlockData(data *common.BlockData) {
	fmt.Println("BlockData")

	fmt.Printf("Num of data: %d\n", len(data.Data))

	var err error
	env := &common.Envelope{}
	if err = proto.Unmarshal(data.Data[0], env); err != nil {
		fmt.Println(err)
		return
	}

	payload := &common.Payload{}
	if err = proto.Unmarshal(env.Payload, payload); err != nil {
		fmt.Println(err)
		return
	}

	transaction := &pb.Transaction{}
	if err = proto.Unmarshal(payload.Data, transaction); err != nil {
		fmt.Println(err)
		return
	}

	fmt.Printf("Num of actions: %d\n", len(transaction.Actions))

	chaincodeActionPayload := &pb.ChaincodeActionPayload{}
	if err = proto.Unmarshal(transaction.Actions[0].Payload, chaincodeActionPayload); err != nil {
		fmt.Println(err)
		return
	}

	proposalResponsePayload := &pb.ProposalResponsePayload{}
	if err := proto.Unmarshal(chaincodeActionPayload.Action.ProposalResponsePayload, proposalResponsePayload); err != nil {
		fmt.Println(err)
		return
	}

	chaincodeAction := &pb.ChaincodeAction{}
	if err = proto.Unmarshal(proposalResponsePayload.Extension, chaincodeAction); err != nil {
		fmt.Println(err)
		return
	}

	txReadWriteSet := &rwset.TxReadWriteSet{}
	if err := proto.Unmarshal(chaincodeAction.Results, txReadWriteSet); err != nil {
		fmt.Println(err)
		return
	}

	fmt.Printf("Num of NsRwset: %d\n", len(txReadWriteSet.NsRwset))

	RwSet := txReadWriteSet.NsRwset[1].Rwset

	fmt.Printf("RwSet: %x\n", RwSet)

	kvrwset := &kvrwset.KVRWSet{}
	if err = proto.Unmarshal(RwSet, kvrwset); err != nil {
		fmt.Println(err)
		return
	}

	fmt.Printf("Num of reads: %d\n", len(kvrwset.Reads))
	fmt.Printf("Num of writes: %d\n", len(kvrwset.Writes))

	if len(kvrwset.Reads) != 0 {
		fmt.Printf("Read: %s\n", kvrwset.Reads[0].Key)
	}

	if len(kvrwset.Writes) != 0 {
		fmt.Printf("Write: %s\n", kvrwset.Writes[0].Key)
	}
}

func RunInvoke0(n *nwo.Network, orderer *nwo.Orderer, peer *nwo.Peer, channel string, params ...string) {
	sess, err := n.PeerUserSession(peer, "User1", commands.ChaincodeInvoke{
		ChannelID: channel,
		Orderer:   n.OrdererAddress(orderer, nwo.ListenPort),
		Name:      "mycc",
		Ctor:      `{"Args":["` + strings.Join(params, `","`) + `"]}`,
		PeerAddresses: []string{
			n.PeerAddress(n.Peer("Org1", "peer0"), nwo.ListenPort),
			n.PeerAddress(n.Peer("Org2", "peer0"), nwo.ListenPort),
		},
		WaitForEvent: true,
	})
	Expect(err).NotTo(HaveOccurred())
	Eventually(sess, n.EventuallyTimeout).Should(gexec.Exit(0))
	Expect(sess.Err).To(gbytes.Say("Chaincode invoke successful. result: status:200"))

	//	sess, err = n.PeerUserSession(peer, "User1", commands.ChaincodeQuery{
	//		ChannelID: channel,
	//		Name:      "mycc",
	//		Ctor:      `{"Args":["query","a"]}`,
	//	})
	//	Expect(err).NotTo(HaveOccurred())
	//	Eventually(sess, n.EventuallyTimeout).Should(gexec.Exit(0))
	//	Expect(sess).To(gbytes.Say("90"))
}

func RunInvoke(n *nwo.Network, orderer *nwo.Orderer, peer *nwo.Peer, channel string, key string, value string) {
	sess, err := n.PeerUserSession(peer, "User1", commands.ChaincodeInvoke{
		ChannelID: channel,
		Orderer:   n.OrdererAddress(orderer, nwo.ListenPort),
		Name:      "mycc",
		Ctor:      `{"Args":["invoke","` + key + `", "` + value + `"]}`,
		PeerAddresses: []string{
			n.PeerAddress(n.Peer("Org1", "peer0"), nwo.ListenPort),
			n.PeerAddress(n.Peer("Org2", "peer0"), nwo.ListenPort),
		},
		WaitForEvent: true,
	})
	Expect(err).NotTo(HaveOccurred())
	Eventually(sess, n.EventuallyTimeout).Should(gexec.Exit(0))
	Expect(sess.Err).To(gbytes.Say("Chaincode invoke successful. result: status:200"))

	//	sess, err = n.PeerUserSession(peer, "User1", commands.ChaincodeQuery{
	//		ChannelID: channel,
	//		Name:      "mycc",
	//		Ctor:      `{"Args":["query","a"]}`,
	//	})
	//	Expect(err).NotTo(HaveOccurred())
	//	Eventually(sess, n.EventuallyTimeout).Should(gexec.Exit(0))
	//	Expect(sess).To(gbytes.Say("90"))
}

func RunInvoke2(n *nwo.Network, orderer *nwo.Orderer, peer *nwo.Peer, channel string, mergeType string, key string, value string) {
	sess, err := n.PeerUserSession(peer, "User1", commands.ChaincodeInvoke{
		ChannelID: channel,
		Orderer:   n.OrdererAddress(orderer, nwo.ListenPort),
		Name:      "mycc",
		Ctor:      `{"Args":["invoke","` + mergeType + `", "` + key + `", "` + value + `"]}`,
		PeerAddresses: []string{
			n.PeerAddress(n.Peer("Org1", "peer0"), nwo.ListenPort),
			n.PeerAddress(n.Peer("Org2", "peer0"), nwo.ListenPort),
		},
		WaitForEvent: true,
	})
	Expect(err).NotTo(HaveOccurred())
	Eventually(sess, n.EventuallyTimeout).Should(gexec.Exit(0))
	Expect(sess.Err).To(gbytes.Say("Chaincode invoke successful. result: status:200"))

	//	sess, err = n.PeerUserSession(peer, "User1", commands.ChaincodeQuery{
	//		ChannelID: channel,
	//		Name:      "mycc",
	//		Ctor:      `{"Args":["query","a"]}`,
	//	})
	//	Expect(err).NotTo(HaveOccurred())
	//	Eventually(sess, n.EventuallyTimeout).Should(gexec.Exit(0))
	//	Expect(sess).To(gbytes.Say("90"))
}

func CheckInvoke(n *nwo.Network, orderer *nwo.Orderer, peer *nwo.Peer, channel string, key string, value string) {
	sess, err := n.PeerUserSession(peer, "User1", commands.ChaincodeQuery{
		ChannelID: channel,
		Name:      "mycc",
		Ctor:      `{"Args":["query","` + key + `"]}`,
	})
	Expect(err).NotTo(HaveOccurred())
	Eventually(sess, n.EventuallyTimeout).Should(gexec.Exit(0))
	Expect(sess).To(gbytes.Say(value))
}

func PrintQueryResponse(n *nwo.Network, orderer *nwo.Orderer, peer *nwo.Peer, channel string, key string) {
	By("querying the chaincode")
	sess, err := n.PeerUserSession(peer, "User1", commands.ChaincodeQuery{
		ChannelID: channel,
		Name:      "mycc",
		Ctor:      `{"Args":["query","` + key + `"]}`,
	})
	Expect(err).NotTo(HaveOccurred())
	Eventually(sess, n.EventuallyTimeout).Should(gexec.Exit(0))
	fmt.Println(key + " value is " + string(sess.Out.Contents()))
	// Expect(sess).To(gbytes.Say("100"))
}

func RunQueryInvokeQuery(n *nwo.Network, orderer *nwo.Orderer, peer *nwo.Peer, channel string) {
	By("querying the chaincode")
	sess, err := n.PeerUserSession(peer, "User1", commands.ChaincodeQuery{
		ChannelID: channel,
		Name:      "mycc",
		Ctor:      `{"Args":["query","a"]}`,
	})
	Expect(err).NotTo(HaveOccurred())
	Eventually(sess, n.EventuallyTimeout).Should(gexec.Exit(0))
	Expect(sess).To(gbytes.Say("100"))

	sess, err = n.PeerUserSession(peer, "User1", commands.ChaincodeInvoke{
		ChannelID: channel,
		Orderer:   n.OrdererAddress(orderer, nwo.ListenPort),
		Name:      "mycc",
		Ctor:      `{"Args":["invoke","a","b","10"]}`,
		PeerAddresses: []string{
			n.PeerAddress(n.Peer("Org1", "peer0"), nwo.ListenPort),
			n.PeerAddress(n.Peer("Org2", "peer0"), nwo.ListenPort),
		},
		WaitForEvent: true,
	})
	Expect(err).NotTo(HaveOccurred())
	Eventually(sess, n.EventuallyTimeout).Should(gexec.Exit(0))
	Expect(sess.Err).To(gbytes.Say("Chaincode invoke successful. result: status:200"))

	sess, err = n.PeerUserSession(peer, "User1", commands.ChaincodeQuery{
		ChannelID: channel,
		Name:      "mycc",
		Ctor:      `{"Args":["query","a"]}`,
	})
	Expect(err).NotTo(HaveOccurred())
	Eventually(sess, n.EventuallyTimeout).Should(gexec.Exit(0))
	Expect(sess).To(gbytes.Say("90"))
}

func RunRespondWith(n *nwo.Network, orderer *nwo.Orderer, peer *nwo.Peer, channel string) {
	By("responding with a 300")
	sess, err := n.PeerUserSession(peer, "User1", commands.ChaincodeInvoke{
		ChannelID: channel,
		Orderer:   n.OrdererAddress(orderer, nwo.ListenPort),
		Name:      "mycc",
		Ctor:      `{"Args":["respond","300","response-message","response-payload"]}`,
		PeerAddresses: []string{
			n.PeerAddress(n.Peer("Org1", "peer0"), nwo.ListenPort),
			n.PeerAddress(n.Peer("Org2", "peer0"), nwo.ListenPort),
		},
		WaitForEvent: true,
	})
	Expect(err).NotTo(HaveOccurred())
	Eventually(sess, n.EventuallyTimeout).Should(gexec.Exit(0))
	Expect(sess.Err).To(gbytes.Say("Chaincode invoke successful. result: status:300"))

	By("responding with a 400")
	sess, err = n.PeerUserSession(peer, "User1", commands.ChaincodeInvoke{
		ChannelID: channel,
		Orderer:   n.OrdererAddress(orderer, nwo.ListenPort),
		Name:      "mycc",
		Ctor:      `{"Args":["respond","400","response-message","response-payload"]}`,
		PeerAddresses: []string{
			n.PeerAddress(n.Peer("Org1", "peer0"), nwo.ListenPort),
			n.PeerAddress(n.Peer("Org2", "peer0"), nwo.ListenPort),
		},
		WaitForEvent: true,
	})
	Expect(err).NotTo(HaveOccurred())
	Eventually(sess, n.EventuallyTimeout).Should(gexec.Exit(1))
	Expect(sess.Err).To(gbytes.Say(`Error: endorsement failure during invoke.`))
}

func CheckPeerStatsdMetrics(prefix string, mr *MetricsReader, timeout time.Duration) {
	By("checking for peer statsd metrics")
	Eventually(mr.String, timeout).Should(SatisfyAll(
		ContainSubstring(prefix+".logging.entries_checked.info:"),
		ContainSubstring(prefix+".logging.entries_written.info:"),
		ContainSubstring(prefix+".go.mem.gc_completed_count:"),
		ContainSubstring(prefix+".grpc.server.unary_requests_received.protos_Endorser.ProcessProposal:"),
		ContainSubstring(prefix+".grpc.server.unary_requests_completed.protos_Endorser.ProcessProposal.OK:"),
		ContainSubstring(prefix+".grpc.server.unary_request_duration.protos_Endorser.ProcessProposal.OK:"),
		ContainSubstring(prefix+".ledger.blockchain_height"),
		ContainSubstring(prefix+".ledger.blockstorage_commit_time"),
		ContainSubstring(prefix+".ledger.blockstorage_and_pvtdata_commit_time"),
	))
}

func CheckPeerStatsdStreamMetrics(mr *MetricsReader, timeout time.Duration) {
	By("checking for stream metrics")
	Eventually(mr.String, timeout).Should(SatisfyAll(
		ContainSubstring(".grpc.server.stream_requests_received.protos_Deliver.DeliverFiltered:"),
		ContainSubstring(".grpc.server.stream_requests_completed.protos_Deliver.DeliverFiltered.Unknown:"),
		ContainSubstring(".grpc.server.stream_request_duration.protos_Deliver.DeliverFiltered.Unknown:"),
		ContainSubstring(".grpc.server.stream_messages_received.protos_Deliver.DeliverFiltered"),
		ContainSubstring(".grpc.server.stream_messages_sent.protos_Deliver.DeliverFiltered"),
	))
}

func CheckOrdererStatsdMetrics(prefix string, mr *MetricsReader, timeout time.Duration) {
	Eventually(mr.String, timeout).Should(SatisfyAll(
		ContainSubstring(prefix+".grpc.server.stream_request_duration.orderer_AtomicBroadcast.Broadcast.OK"),
		ContainSubstring(prefix+".grpc.server.stream_request_duration.orderer_AtomicBroadcast.Deliver."),
		ContainSubstring(prefix+".logging.entries_checked.info:"),
		ContainSubstring(prefix+".logging.entries_written.info:"),
		ContainSubstring(prefix+".go.mem.gc_completed_count:"),
		ContainSubstring(prefix+".grpc.server.stream_requests_received.orderer_AtomicBroadcast.Deliver:"),
		ContainSubstring(prefix+".grpc.server.stream_requests_completed.orderer_AtomicBroadcast.Deliver."),
		ContainSubstring(prefix+".grpc.server.stream_messages_received.orderer_AtomicBroadcast.Deliver"),
		ContainSubstring(prefix+".grpc.server.stream_messages_sent.orderer_AtomicBroadcast.Deliver"),
		ContainSubstring(prefix+".ledger.blockchain_height"),
		ContainSubstring(prefix+".ledger.blockstorage_commit_time"),
	))
}

func CheckPeerOperationEndpoints(network *nwo.Network, peer *nwo.Peer) {
	metricsURL := fmt.Sprintf("https://127.0.0.1:%d/metrics", network.PeerPort(peer, nwo.OperationsPort))
	logspecURL := fmt.Sprintf("https://127.0.0.1:%d/logspec", network.PeerPort(peer, nwo.OperationsPort))
	healthURL := fmt.Sprintf("https://127.0.0.1:%d/healthz", network.PeerPort(peer, nwo.OperationsPort))

	authClient, unauthClient := nwo.PeerOperationalClients(network, peer)

	CheckPeerPrometheusMetrics(authClient, metricsURL)
	CheckLogspecOperations(authClient, logspecURL)
	CheckHealthEndpoint(authClient, healthURL)

	By("getting the logspec without a client cert")
	resp, err := unauthClient.Get(logspecURL)
	Expect(err).NotTo(HaveOccurred())
	Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))

	By("ensuring health checks do not require a client cert")
	CheckHealthEndpoint(unauthClient, healthURL)
}

func CheckOrdererOperationEndpoints(network *nwo.Network, orderer *nwo.Orderer) {
	metricsURL := fmt.Sprintf("https://127.0.0.1:%d/metrics", network.OrdererPort(orderer, nwo.OperationsPort))
	logspecURL := fmt.Sprintf("https://127.0.0.1:%d/logspec", network.OrdererPort(orderer, nwo.OperationsPort))
	healthURL := fmt.Sprintf("https://127.0.0.1:%d/healthz", network.OrdererPort(orderer, nwo.OperationsPort))

	authClient, unauthClient := nwo.OrdererOperationalClients(network, orderer)

	CheckOrdererPrometheusMetrics(authClient, metricsURL)
	CheckLogspecOperations(authClient, logspecURL)
	CheckHealthEndpoint(authClient, healthURL)

	By("getting the logspec without a client cert")
	resp, err := unauthClient.Get(logspecURL)
	Expect(err).NotTo(HaveOccurred())
	Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))

	By("ensuring health checks do not require a client cert")
	CheckHealthEndpoint(unauthClient, healthURL)
}

func CheckPeerPrometheusMetrics(client *http.Client, url string) {
	By("hitting the prometheus metrics endpoint")
	resp, err := client.Get(url)
	Expect(err).NotTo(HaveOccurred())
	Expect(resp.StatusCode).To(Equal(http.StatusOK))
	resp.Body.Close()

	Eventually(getBody(client, url)).Should(ContainSubstring(`# TYPE grpc_server_stream_request_duration histogram`))

	By("checking for some expected metrics")
	body := getBody(client, url)()
	Expect(body).To(ContainSubstring(`# TYPE go_gc_duration_seconds summary`))
	Expect(body).To(ContainSubstring(`# TYPE grpc_server_stream_request_duration histogram`))
	Expect(body).To(ContainSubstring(`grpc_server_stream_request_duration_count{code="Unknown",method="DeliverFiltered",service="protos_Deliver"}`))
	Expect(body).To(ContainSubstring(`grpc_server_stream_messages_received{method="DeliverFiltered",service="protos_Deliver"}`))
	Expect(body).To(ContainSubstring(`grpc_server_stream_messages_sent{method="DeliverFiltered",service="protos_Deliver"}`))
	Expect(body).To(ContainSubstring(`# TYPE grpc_comm_conn_closed counter`))
	Expect(body).To(ContainSubstring(`# TYPE grpc_comm_conn_opened counter`))
	Expect(body).To(ContainSubstring(`ledger_blockchain_height`))
	Expect(body).To(ContainSubstring(`ledger_blockstorage_commit_time_bucket`))
	Expect(body).To(ContainSubstring(`ledger_blockstorage_and_pvtdata_commit_time_bucket`))
}

func CheckOrdererPrometheusMetrics(client *http.Client, url string) {
	By("hitting the prometheus metrics endpoint")
	resp, err := client.Get(url)
	Expect(err).NotTo(HaveOccurred())
	Expect(resp.StatusCode).To(Equal(http.StatusOK))
	resp.Body.Close()

	Eventually(getBody(client, url)).Should(ContainSubstring(`# TYPE grpc_server_stream_request_duration histogram`))

	By("checking for some expected metrics")
	body := getBody(client, url)()
	Expect(body).To(ContainSubstring(`# TYPE go_gc_duration_seconds summary`))
	Expect(body).To(ContainSubstring(`# TYPE grpc_server_stream_request_duration histogram`))
	Expect(body).To(ContainSubstring(`grpc_server_stream_request_duration_sum{code="OK",method="Deliver",service="orderer_AtomicBroadcast"`))
	Expect(body).To(ContainSubstring(`grpc_server_stream_request_duration_sum{code="OK",method="Broadcast",service="orderer_AtomicBroadcast"`))
	Expect(body).To(ContainSubstring(`# TYPE grpc_comm_conn_closed counter`))
	Expect(body).To(ContainSubstring(`# TYPE grpc_comm_conn_opened counter`))
	Expect(body).To(ContainSubstring(`ledger_blockchain_height`))
	Expect(body).To(ContainSubstring(`ledger_blockstorage_commit_time_bucket`))
}

func CheckLogspecOperations(client *http.Client, logspecURL string) {
	By("getting the logspec")
	resp, err := client.Get(logspecURL)
	Expect(err).NotTo(HaveOccurred())
	Expect(resp.StatusCode).To(Equal(http.StatusOK))
	bodyBytes, err := ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	Expect(err).NotTo(HaveOccurred())
	Expect(string(bodyBytes)).To(MatchJSON(`{"spec":"info"}`))

	updateReq, err := http.NewRequest(http.MethodPut, logspecURL, strings.NewReader(`{"spec":"debug"}`))
	Expect(err).NotTo(HaveOccurred())

	By("setting the logspec")
	resp, err = client.Do(updateReq)
	Expect(err).NotTo(HaveOccurred())
	Expect(resp.StatusCode).To(Equal(http.StatusNoContent))
	resp.Body.Close()

	resp, err = client.Get(logspecURL)
	Expect(err).NotTo(HaveOccurred())
	Expect(resp.StatusCode).To(Equal(http.StatusOK))
	bodyBytes, err = ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	Expect(err).NotTo(HaveOccurred())
	Expect(string(bodyBytes)).To(MatchJSON(`{"spec":"debug"}`))

	By("resetting the logspec")
	updateReq, err = http.NewRequest(http.MethodPut, logspecURL, strings.NewReader(`{"spec":"info"}`))
	Expect(err).NotTo(HaveOccurred())
	resp, err = client.Do(updateReq)
	Expect(err).NotTo(HaveOccurred())
	Expect(resp.StatusCode).To(Equal(http.StatusNoContent))
	resp.Body.Close()
}

func CheckHealthEndpoint(client *http.Client, url string) {
	body := getBody(client, url)()

	var healthStatus healthz.HealthStatus
	err := json.Unmarshal([]byte(body), &healthStatus)
	Expect(err).NotTo(HaveOccurred())
	Expect(healthStatus.Status).To(Equal(healthz.StatusOK))
}

func getBody(client *http.Client, url string) func() string {
	return func() string {
		resp, err := client.Get(url)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		bodyBytes, err := ioutil.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		resp.Body.Close()
		return string(bodyBytes)
	}
}

func packageInstallApproveChaincode(network *nwo.Network, channel string, orderer *nwo.Orderer, chaincode nwo.Chaincode, peers ...*nwo.Peer) {
	nwo.PackageChaincode(network, chaincode, peers[0])
	nwo.InstallChaincode(network, chaincode, peers...)
	nwo.ApproveChaincodeForMyOrg(network, channel, orderer, chaincode, peers...)
}

func hashFile(file string) string {
	f, err := os.Open(file)
	Expect(err).NotTo(HaveOccurred())
	defer f.Close()

	h := sha256.New()
	_, err = io.Copy(h, f)
	Expect(err).NotTo(HaveOccurred())

	return fmt.Sprintf("%x", h.Sum(nil))
}

func chaincodeContainerNameFilter(n *nwo.Network, chaincode nwo.Chaincode) string {
	return fmt.Sprintf("^/%s-.*-%s-%s$", n.NetworkID, chaincode.Label, hashFile(chaincode.PackageFile))
}
