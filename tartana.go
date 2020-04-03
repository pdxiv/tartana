package gonetworktest

// Commonly used functions
import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"syscall"
)

// ConfigFile contains the name of the JSON file containing config för the application
const ConfigFile = "conf.json"

// BufferAllocationSize sets the amount of space we-pre-allocate for sending and receiving network data
const BufferAllocationSize = 65507

// SendQueueSizeInitialSize denotes the initial size of the send queue
const SendQueueSizeInitialSize = 16

// Configuration is for handling configuration parameters
type Configuration struct {
	HubSinkAddress string
	HubRiseAddress string
	AppSinkAddress string
	AppRiseAddress string
	GobRiseAddress string
	GobSinkAddress string
	GobTCPAddress  string
	// MaxSendsInFlight defines the maximum number of un-acknowledged sends that are allowed
	MaxSendsInFlight int
}

// AppCommData is for handling communication from an App to the Hub
type AppCommData struct {
	// Actual data as native data types
	Type                      uint16
	PayloadSize               uint16
	ID                        uint64
	AppSequenceNumber         uint64
	ExpectedAppSequenceNumber uint64
	// Temporary buffer storage for data
	TypeBuffer              []byte
	SizeBuffer              []byte
	IDBuffer                []byte
	AppSequenceNumberBuffer []byte
	Payload                 []byte
	// The actual data as bytes that will be sent over UDP
	MasterBuffer []byte
}

// HubCommData is for handling communication from a Hub to the Apps
type HubCommData struct {
	// Actual data as native data types
	SessionID                 uint64
	HubSequenceNumber         uint64
	NumberOfAppPayloads       uint16 // If we put together several App in one Hub
	ExpectedHubSequenceNumber uint64
	// Temporary buffer storage for data
	SessionIDBuffer           []byte
	HubSequenceNumberBuffer   []byte
	NumberOfAppPayloadsBuffer []byte
	Payload                   []byte
	// The actual data as bytes that will be sent over UDP
	MasterBuffer []byte
}

// AppState handles the internal state of an App, especially regarding sending data
type AppState struct {
	ID                uint64
	QueueEntries      uint
	QueueHeadLocation uint
	QueueCapacity     uint
	InFlightMarker    uint
	SendQueue         [][]byte
}

// InitAppMessage initializes all the message parameters
func InitAppMessage(data *AppCommData) {
	data.Type = 0
	data.PayloadSize = 0
	data.ID = 0
	data.AppSequenceNumber = 0
	data.ExpectedAppSequenceNumber = 0
	data.TypeBuffer = make([]byte, 2)
	data.SizeBuffer = make([]byte, 2)
	data.IDBuffer = make([]byte, 8)
	data.AppSequenceNumberBuffer = make([]byte, 8)
	data.Payload = make([]byte, 0, BufferAllocationSize)
	data.MasterBuffer = make([]byte, 0, BufferAllocationSize)
}

// InitHubMessage initializes all the message parameters
func InitHubMessage(data *HubCommData) {
	data.SessionID = 31337
	data.HubSequenceNumber = 0
	data.NumberOfAppPayloads = 1 // To begin with only ever 1 App in one Hub msg
	data.ExpectedHubSequenceNumber = 0
	data.SessionIDBuffer = make([]byte, 8)
	data.HubSequenceNumberBuffer = make([]byte, 8)
	data.NumberOfAppPayloadsBuffer = make([]byte, 2)
	data.Payload = make([]byte, 0, BufferAllocationSize)
	data.MasterBuffer = make([]byte, 0, BufferAllocationSize)
}

// InitAppState initializes the data structure for an App state
func InitAppState(ID uint64) AppState {
	var state AppState
	state.ID = ID
	state.QueueEntries = 0
	state.QueueHeadLocation = 0
	state.QueueCapacity = SendQueueSizeInitialSize
	state.InFlightMarker = 0
	state.SendQueue = make([][]byte, SendQueueSizeInitialSize)
	for i := range state.SendQueue {
		state.SendQueue[i] = make([]byte, BufferAllocationSize)
	}
	return state
}

// DecodeHubMessage decodes the bytes in a message from a Hub
func DecodeHubMessage(data *HubCommData) bool {
	data.SessionID = binary.BigEndian.Uint64(data.MasterBuffer[0:8])
	data.HubSequenceNumber = binary.BigEndian.Uint64(data.MasterBuffer[8:16])
	data.NumberOfAppPayloads = binary.BigEndian.Uint16(data.MasterBuffer[16:18])
	data.Payload = data.MasterBuffer[18:]
	/*
		Here's how the gap detection should work for an App listening to Hub:
		- At initialization, set ExpectedHubSequenceNumber to 0
		- Read the Hub message, and decode HubSequenceNumber
		- if ExpectedHubSequenceNumber == HubSequenceNumber then
		-   increment ExpectedHubSequenceNumber
		- else
		-   report a sequence number gap, and ask for the gaps to be filled
		-   ExpectedHubSequenceNumber = HubSequenceNumber + 1

		Hub sequence number handling should have three possible scenarios:
		- expected sequence number - continue
		- higher sequence number than expected - report gap, request lost data
		- lower sequence number than expected - do nothing
	*/

	if data.ExpectedHubSequenceNumber < data.HubSequenceNumber {
		// Here we should have code to fill gaps from a "gob"
		fmt.Println("**************** Sequence number", data.HubSequenceNumber, "not expected. Too high. Expecting", data.ExpectedHubSequenceNumber, "We should try to re-fetch ", data.ExpectedHubSequenceNumber, "-", data.HubSequenceNumber-1, "before continuing.")
		data.ExpectedHubSequenceNumber = data.HubSequenceNumber + 1 // Just continue without missing data, for now
		return true
		// return false
	} else if data.ExpectedHubSequenceNumber != data.HubSequenceNumber {
		// Do nothing, and wait for the sequence numbers to catch up.
		fmt.Println("**************** Sequence number", data.HubSequenceNumber, "not expected. Too low. Expecting", data.ExpectedHubSequenceNumber)
		return false
	}
	// fmt.Println("Hub session:", data.SessionID)
	// fmt.Println("Hub sequence:", data.HubSequenceNumber)
	// fmt.Println("Hub App payloads:", data.NumberOfAppPayloads)
	return true
}

// HubDecodeAppMessage decodes the bytes in a message from an App
func HubDecodeAppMessage(data *AppCommData, expectedSequenceForApp *map[uint64]uint64) bool {
	data.Type = binary.BigEndian.Uint16(data.MasterBuffer[0:2])
	data.PayloadSize = binary.BigEndian.Uint16(data.MasterBuffer[2:4])
	data.ID = binary.BigEndian.Uint64(data.MasterBuffer[4:12])
	data.AppSequenceNumber = binary.BigEndian.Uint64(data.MasterBuffer[12:20])
	data.Payload = data.MasterBuffer[20 : 20+data.PayloadSize]

	/*
		Here's how the Hub gap handling should work:
		- At initialization, set ExpectedAppSequenceNumber to 0
		- Read the App message, and decode AppSequenceNumber
		- if ExpectedAppSequenceNumber == AppSequenceNumber then
		-   increment ExpectedAppSequenceNumber
		- else
		-   ignore App message

		App sequence number handling should have two possible scenarios:
		- expected sequence number - continue
		- lower sequence number than expected - do nothing
	*/

	if _, ok := (*expectedSequenceForApp)[data.ID]; ok {
		fmt.Println("found pre-existing entry for app id", data.ID)
	} else {
		fmt.Println("couldn't find a previous entry for app id", data.ID)
		(*expectedSequenceForApp)[data.ID] = 0
	}

	if (*expectedSequenceForApp)[data.ID] != data.AppSequenceNumber {
		// Do nothing, and wait for the sequence numbers to catch up.
		fmt.Println("**************** Sequence number", data.AppSequenceNumber, "not expected. Expecting", (*expectedSequenceForApp)[data.ID])
		return false
	}
	fmt.Println("App type:", data.Type)
	fmt.Println("App size:", data.PayloadSize)
	fmt.Println("App id:", data.ID)
	fmt.Println("App sequence number:", data.AppSequenceNumber)
	fmt.Printf("App payload: \"%s\"\n", string(data.Payload))
	(*expectedSequenceForApp)[data.ID]++
	return true

}

// AppDecodeAppMessage decodes the bytes in a message from an App
func AppDecodeAppMessage(data *AppCommData) bool {
	data.Type = binary.BigEndian.Uint16(data.MasterBuffer[0:2])
	data.PayloadSize = binary.BigEndian.Uint16(data.MasterBuffer[2:4])
	data.ID = binary.BigEndian.Uint64(data.MasterBuffer[4:12])
	data.AppSequenceNumber = binary.BigEndian.Uint64(data.MasterBuffer[12:20])
	data.Payload = data.MasterBuffer[20 : 20+data.PayloadSize]
	return true
}

// SendAppMessage encodes as bytes and send an App message to the hub
func SendAppMessage(data *AppCommData, connection *net.UDPConn) {
	// Clear data buffers
	data.MasterBuffer = data.MasterBuffer[:0] // Clear the byte slice send buffer

	data.PayloadSize = uint16(len(data.Payload))

	// Convert fields into byte arrays
	binary.BigEndian.PutUint16(data.TypeBuffer, data.Type)
	binary.BigEndian.PutUint16(data.SizeBuffer, data.PayloadSize)
	binary.BigEndian.PutUint64(data.IDBuffer, data.ID)
	binary.BigEndian.PutUint64(data.AppSequenceNumberBuffer, data.AppSequenceNumber)

	// Add byte arrays to master output buffer
	data.MasterBuffer = append(data.MasterBuffer, data.TypeBuffer...)
	data.MasterBuffer = append(data.MasterBuffer, data.SizeBuffer...)
	data.MasterBuffer = append(data.MasterBuffer, data.IDBuffer...)
	data.MasterBuffer = append(data.MasterBuffer, data.AppSequenceNumberBuffer...)

	// Add payload to master output buffer
	data.MasterBuffer = append(data.MasterBuffer, data.Payload...)

	connection.Write(data.MasterBuffer)
	data.AppSequenceNumber++ // Increment App sequence number every time we've sent a datagram
}

// GetConfiguration fetches configuration parameters from JSON file
func GetConfiguration(filename string) Configuration {
	file, _ := os.Open(filename)
	defer file.Close()
	decoder := json.NewDecoder(file)
	configuration := Configuration{}
	err := decoder.Decode(&configuration)
	if err != nil {
		fmt.Println("error:", err)
	}
	return configuration
}

// SendHubMessage encodes as bytes and send a Hub message to the apps
func SendHubMessage(sinkData *AppCommData, riseData *HubCommData, connection *net.UDPConn) {

	fmt.Println("riseData.NumberOfAppPayloads", riseData.NumberOfAppPayloads)
	// Clear riseData buffers
	riseData.MasterBuffer = riseData.MasterBuffer[:0] // Clear the byte slice send buffer

	// Convert fields into byte arrays
	binary.BigEndian.PutUint64(riseData.SessionIDBuffer, riseData.SessionID)
	binary.BigEndian.PutUint64(riseData.HubSequenceNumberBuffer, riseData.HubSequenceNumber)
	binary.BigEndian.PutUint16(riseData.NumberOfAppPayloadsBuffer, riseData.NumberOfAppPayloads)

	// Add byte arrays to master output buffer
	riseData.MasterBuffer = append(riseData.MasterBuffer, riseData.SessionIDBuffer...)
	riseData.MasterBuffer = append(riseData.MasterBuffer, riseData.HubSequenceNumberBuffer...)
	riseData.MasterBuffer = append(riseData.MasterBuffer, riseData.NumberOfAppPayloadsBuffer...)

	// Add payload to master output buffer
	appDataSize := sinkData.PayloadSize + 20 // Size of App packet
	riseData.MasterBuffer = append(riseData.MasterBuffer, sinkData.MasterBuffer[0:appDataSize]...)
	connection.Write(riseData.MasterBuffer)
	riseData.HubSequenceNumber++ // Increment App sequence number every time we've sent a datagram
}

// ControlOnConnSetupSoReusePort creates network setup for SO_REUSEPORT
func ControlOnConnSetupSoReusePort(network string, address string, c syscall.RawConn) error {
	var operr error
	var fn = func(s uintptr) {
		operr = syscall.SetsockoptInt(int(s), syscall.SOL_SOCKET, 0xF /* syscall.SO_REUSE_PORT */, 1)
	}
	if err := c.Control(fn); err != nil {
		return err
	}
	if operr != nil {
		return operr
	}
	return nil
}
