/**
 * Copyright 2018-19 TIBCO Software Inc. All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License"); You may not use this file except
 * in compliance with the License.
 * A copy of the License is included in the distribution package with this file.
 * You also may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * File name: AbstractChannel.go
 * Created on: Dec 01, 2018
 * Created by: achavan
 * SVN id: $id: $
 *
 */

package channel

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"github.com/TIBCOSoftware/tgdb-client/client/goAPI/exception"
	"github.com/TIBCOSoftware/tgdb-client/client/goAPI/pdu"
	"github.com/TIBCOSoftware/tgdb-client/client/goAPI/types"
	"github.com/TIBCOSoftware/tgdb-client/client/goAPI/utils"
	"sync"
	"sync/atomic"
	"time"
)

// ======= Exception channel Type =======
type ExceptionChannelType int

const (
	RethrowException ExceptionChannelType = iota
	RetryOperation
	Disconnected
)

type ExceptionHandleResult struct {
	ExceptionType    ExceptionChannelType // types.TGExceptionType
	ExceptionMessage string
}

func (proType ExceptionChannelType) ChannelException() *ExceptionHandleResult {
	// Use a buffer for efficient string concatenation
	var exceptionResult ExceptionHandleResult

	if proType&RethrowException == RethrowException {
		exceptionResult = ExceptionHandleResult{
			ExceptionType:    types.TGErrorGeneralException,
			ExceptionMessage: "TGDB-CHANNEL-FAIL:Failed to reconnect",
		}
	}
	if proType&RetryOperation == RetryOperation {
		exceptionResult = ExceptionHandleResult{
			ExceptionType:    types.TGErrorRetryIOException,
			ExceptionMessage: "TGDB-CHANNEL-RETRY:Channel Reconnected, Retry Operation",
		}
	}
	if proType&Disconnected == Disconnected {
		exceptionResult = ExceptionHandleResult{
			ExceptionType:    types.TGErrorChannelDisconnected,
			ExceptionMessage: "TGDB-CHANNEL-FAIL:Failed to reconnect",
		}
	}
	return &exceptionResult
}

var ConnectionsToChannel int32

type AbstractChannel struct {
	authToken         int64
	channelLinkState  types.LinkState
	channelProperties *utils.SortedProperties
	channelUrl        *LinkUrl
	clientId          string
	connectionIndex   int
	cryptographer     types.TGDataCryptoGrapher
	inboxAddress      string
	needsPing         bool
	numOfConnections  int32
	lastActiveTime    time.Time
	primaryUrl        *LinkUrl
	reader            *ChannelReader
	requestId         int64
	responses         map[int64]types.TGChannelResponse
	sessionId         int64
	exceptionLock     sync.Mutex // reentrant-lock for synchronizing sending/receiving messages over the wire
	exceptionCond     *sync.Cond // Condition for lock
	sendLock          sync.Mutex // reentrant-lock for synchronizing sending/receiving messages over the wire
	tracer            types.TGTracer // Used for tracing the information flow during the execution
}

func DefaultAbstractChannel() *AbstractChannel {
	// We must register the concrete type for the encoder and decoder (which would
	// normally be on a separate machine from the encoder). On each end, this tells the
	// engine which concrete type is being sent that implements the interface.
	gob.Register(AbstractChannel{})

	newChannel := AbstractChannel{
		authToken:        -1,
		connectionIndex:  0,
		needsPing:        false,
		numOfConnections: 0,
		lastActiveTime:   time.Now(),
		channelLinkState: types.LinkNotConnected,
		channelUrl:       DefaultLinkUrl(),
		primaryUrl:       DefaultLinkUrl(),
		responses:        make(map[int64]types.TGChannelResponse, 0),
		sessionId:        -1,
	}
	newChannel.exceptionCond = sync.NewCond(&newChannel.exceptionLock) // Condition for lock
	newChannel.reader = NewChannelReader(&newChannel)
	return &newChannel
}

func NewAbstractChannel(linkUrl *LinkUrl, props *utils.SortedProperties) *AbstractChannel {
	newChannel := DefaultAbstractChannel()
	newChannel.channelUrl = linkUrl
	newChannel.primaryUrl = linkUrl
	newChannel.channelProperties = props
	// TODO: Uncomment the following two lines to test Trace functionality
	//enableTraceFlag := newChannel.channelProperties.GetProperty(utils.GetConfigFromKey(utils.EnableConnectionTrace), "true")
	//if enableTraceFlag == "true" {
	enableTraceFlag := newChannel.channelProperties.GetPropertyAsBoolean(utils.GetConfigFromKey(utils.EnableConnectionTrace))
	if enableTraceFlag {
		traceDir := newChannel.channelProperties.GetProperty(utils.GetConfigFromKey(utils.ConnectionTraceDir), ".")
		clientId := newChannel.channelProperties.GetProperty(utils.GetConfigFromKey(utils.ChannelClientId), "")
		newChannel.tracer = NewChannelTracer(clientId, traceDir)
		//newChannel.tracer.Start()
	}
	return newChannel
}

/////////////////////////////////////////////////////////////////
// Private functions for TGChannel / Derived Channels
/////////////////////////////////////////////////////////////////

func getChannelClientProtocolVersion() uint16 {
	return utils.GetProtocolVersion()
}

func getServerProtocolVersion() uint16 {
	return 0
}

func isChannelClosing(obj types.TGChannel) bool {
	if obj.GetLinkState() == types.LinkClosing {
		return true
	}
	return false
}

func isChannelClosed(obj types.TGChannel) bool {
	if obj.GetLinkState() == types.LinkClosing || obj.GetLinkState() == types.LinkClosed || obj.GetLinkState() == types.LinkTerminated {
		return true
	}
	return false
}

func isChannelConnected(obj types.TGChannel) bool {
	if obj.GetLinkState() == types.LinkConnected {
		return true
	}
	return false
}

func (obj *AbstractChannel) channelToString() string {
	var buffer bytes.Buffer
	buffer.WriteString("AbstractChannel:{")
	buffer.WriteString(fmt.Sprintf("AuthToken: %d", obj.authToken))
	//buffer.WriteString(fmt.Sprintf(", ChannelProperties: %+v", obj.ChannelProperties))
	buffer.WriteString(fmt.Sprintf(", ClientId: %s", obj.clientId))
	buffer.WriteString(fmt.Sprintf(", ConnectionIndex: %d", obj.connectionIndex))
	//buffer.WriteString(fmt.Sprintf(", DataCryptoGrapher: %+v", obj.cryptoGrapher))
	buffer.WriteString(fmt.Sprintf(", InboxAddress: %s", obj.inboxAddress))
	buffer.WriteString(fmt.Sprintf(", NeedsPing: %+v", obj.needsPing))
	buffer.WriteString(fmt.Sprintf(", NumOfConnections: %d", obj.numOfConnections))
	buffer.WriteString(fmt.Sprintf(", LastActiveTime: %+v", obj.lastActiveTime))
	buffer.WriteString(fmt.Sprintf(", LinkState: %s", obj.channelLinkState.String()))
	buffer.WriteString(fmt.Sprintf(", ChannelUrl: %s", obj.channelUrl.String()))
	buffer.WriteString(fmt.Sprintf(", PrimaryUrl: %s", obj.primaryUrl.String()))
	buffer.WriteString(fmt.Sprintf(", RequestId: %d", obj.requestId))
	buffer.WriteString(fmt.Sprintf(", Responses: %+v", obj.responses))
	buffer.WriteString(fmt.Sprintf(", SessionId: %d", obj.sessionId))
	//buffer.WriteString(fmt.Sprintf(", Reader: %s", obj.GetReader().String()))
	//buffer.WriteString(fmt.Sprintf(", Tracer: %s", obj.GetTracer().String()))
	buffer.WriteString(fmt.Sprintf(", ExceptionCond: %+v", obj.exceptionCond))
	buffer.WriteString("}")
	return buffer.String()
}

func (obj *AbstractChannel) getChannelPassword() []byte {
	pwd := ""
	if len(obj.channelProperties.GetAllProperties()) > 0 {
		pwd = obj.channelProperties.GetProperty(utils.GetConfigFromKey(utils.ChannelPassword), "")
	}
	return []byte(pwd)
}

func (obj *AbstractChannel) getChannelUserName() string {
	user := ""
	if len(obj.channelProperties.GetAllProperties()) > 0 {
		user = obj.channelProperties.GetProperty(utils.GetConfigFromKey(utils.ChannelUserID), "")
	}
	return user
}

func (obj *AbstractChannel) isChannelPingable() bool {
	return obj.needsPing
}

func (obj *AbstractChannel) setChannelAuthToken(authToken int64) {
	obj.authToken = authToken
}

func (obj *AbstractChannel) setChannelClientId(clientId string) {
	obj.clientId = clientId
}

func (obj *AbstractChannel) setChannelInboxAddr(addr string) {
	obj.inboxAddress = addr
}

func (obj *AbstractChannel) setChannelSessionId(sessionId int64) {
	obj.sessionId = sessionId
}

// SetDataCryptoGrapher sets the data cryptographer
func (obj *AbstractChannel) setDataCryptoGrapher(crypto types.TGDataCryptoGrapher) {
	obj.cryptographer = crypto
}

func (obj *AbstractChannel) setNoOfConnections(num int32) {
	obj.numOfConnections = num
}

/////////////////////////////////////////////////////////////////
// Helper (Quite Involved) functions for AbstractChannel
/////////////////////////////////////////////////////////////////

func channelConnect(obj types.TGChannel) types.TGError {
	//logger.Log(fmt.Sprintf("Entering AbstractChannel:channelConnect"))
	if isChannelConnected(obj) {
		logger.Log(fmt.Sprintf("AbstractChannel:channelConnect channel is already connected"))
		obj.SetNoOfConnections(atomic.AddInt32(&ConnectionsToChannel, 1))
		return nil
	}
	if isChannelClosed(obj) || obj.GetLinkState() == types.LinkNotConnected {
		logger.Debug(fmt.Sprintf("Inside AbstractChannel:channelConnect about to channelTryRepeatConnect for object '%+v'", obj.String()))
		err := channelTryRepeatConnect(obj, false)
		if err != nil {
			logger.Error(fmt.Sprintf("ERROR: AbstractChannel:channelConnect channelTryRepeatConnect failed w/ '%+v'", err.Error()))
			return err
		}
		obj.SetChannelLinkState(types.LinkConnected)
		obj.SetNoOfConnections(atomic.AddInt32(&ConnectionsToChannel, 1))
		logger.Log(fmt.Sprintf("Returning AbstractChannel:channelConnect successfully established socket connection and now has '%d' number of connections", obj.GetNoOfConnections()))
	} else {
		logger.Error(fmt.Sprintf("ERROR: AbstractChannel:channelConnect channelTryRepeatConnect - connect called on an invalid state := '%s'", obj.GetLinkState().String()))
		errMsg := fmt.Sprintf("Connect called on an invalid state := '%s'", obj.GetLinkState().String())
		return exception.NewTGGeneralExceptionWithMsg(errMsg)
	}
	//logger.Log(fmt.Sprintf("Returning AbstractChannel:channelConnect having '%d' number of connections", obj.GetNoOfConnections()))
	return nil
}

func channelDisConnect(obj types.TGChannel) types.TGError {
	logger.Log(fmt.Sprintf("Entering AbstractChannel:channelDisConnect"))
	obj.ChannelLock()
	defer obj.ChannelUnlock()

	if !isChannelConnected(obj) {
		logger.Warning(fmt.Sprintf("WARNING: Inside AbstractChannel:channelDisConnect channel is already disconnected"))
		return nil
	}

	if obj.GetNoOfConnections() == 0 {
		logger.Warning(fmt.Sprintf("WARNING: Inside AbstractChannel:channelDisConnect calling disconnect more than number of connects"))
		return nil
	}
	obj.SetNoOfConnections(atomic.AddInt32(&ConnectionsToChannel, -1))
	logger.Log(fmt.Sprintf("Returning AbstractChannel:channelDisConnect"))
	return nil
}

func channelHandleException(obj types.TGChannel, ex types.TGError, bReconnect bool) *ExceptionHandleResult {
	logger.Log(fmt.Sprintf("Entering AbstractChannel:channelHandleException w/ Error: '%+v' and Reconnect Flag: '%+v'", ex, bReconnect))
	obj.ExceptionLock()
	defer func() {
		if bReconnect {
			logger.Debug(fmt.Sprint("Inside AbstractChannel:channelHandleException about to obj.exceptionCond.Broadcast()"))
			obj.GetExceptionCondition().Broadcast()
		}
		obj.ExceptionUnlock()
	} ()

	if ex.GetErrorType() != types.TGErrorIOException {
		logger.Debug(fmt.Sprint("Returning AbstractChannel:channelHandleException w/ RethrowException"))
		return RethrowException.ChannelException()
	}

	connectionOpTimeout := obj.GetProperties().GetPropertyAsInt(utils.GetConfigFromKey(utils.ConnectionOperationTimeoutSeconds))

	for {
		logger.Debug(fmt.Sprint("Entering AbstractChannel:channelHandleException Infinite Loop"))
		if bReconnect {
			logger.Debug(fmt.Sprint("Returning AbstractChannel:channelHandleException Infinite Loop"))
			break
		}
		logger.Debug(fmt.Sprint("Inside AbstractChannel:channelHandleException Infinite Loop about to obj.exceptionCond.Wait()"))
		//obj.GetExceptionCondition().Wait()
		time.Sleep(time.Duration(connectionOpTimeout) * time.Second)
		//obj.GetExceptionCondition().Broadcast()
		logger.Debug(fmt.Sprint("Inside AbstractChannel:channelHandleException Infinite Loop about to check isChannelConnected()"))

		if isChannelConnected(obj) {
			logger.Log(fmt.Sprint("Returning AbstractChannel:channelHandleException Infinite Loop w/ RetryOperation as channel is connected"))
			return RetryOperation.ChannelException()
		}
		logger.Debug(fmt.Sprint("Inside AbstractChannel:channelHandleException Infinite Loop about to check IsClosed()"))
		if obj.IsClosed() {
			logger.Log(fmt.Sprint("Returning AbstractChannel:channelHandleException Infinite Loop w/ DisconnectedException as channel is closed"))
			return Disconnected.ChannelException()
		}
	} // End of Infinite Loop

	logger.Debug(fmt.Sprintf("Inside AbstractChannel:channelHandleException about to obj.channelReconnect()"))
	if channelReconnect(obj) {
		logger.Log(fmt.Sprint("Returning AbstractChannel:channelHandleException w/ RetryOperation as failure in channelReconnect()"))
		return RetryOperation.ChannelException()
	}
	logger.Log(fmt.Sprintf("Returning AbstractChannel:channelHandleException w/ DisconnectedException for input exception: '%+v' and Reconnect Flag: '%+v'", ex, bReconnect))
	return Disconnected.ChannelException()
}

// channelProcessMessage processes a message received on the channel. This is called from the ChannelReader.
func channelProcessMessage(obj types.TGChannel, msg types.TGMessage) types.TGError {
	logger.Log(fmt.Sprint("Entering AbstractChannel:channelProcessMessage"))
	reqId := msg.GetRequestId()
	channelResponseMap := obj.GetResponses()
	channelResponse := channelResponseMap[reqId]

	if channelResponse == nil {
		errMsg := fmt.Sprintf("AbstractChannel:channelProcessMessage - Received no response message for corresponding request :%d", reqId)
		logger.Error(fmt.Sprintf("ERROR: Returning %s", errMsg))
		//return exception.GetErrorByType(types.TGErrorGeneralException, types.TGDB_CHANNEL_ERROR, errMsg, "")
		return nil
	}

	logger.Debug(fmt.Sprint("Inside AbstractChannel:channelProcessMessage about to channelResponse.SetReply() w/ MSG"))
	channelResponse.SetReply(msg)

	logger.Log(fmt.Sprintf("Returning AbstractChannel:channelProcessMessage"))
	return nil
}

func channelReconnect(obj types.TGChannel) bool {
	logger.Log(fmt.Sprintf("Entering AbstractChannel:channelReconnect"))
	cn1 := utils.GetConfigFromKey(utils.ChannelFTHosts)
	ftHosts := obj.GetProperties().GetProperty(cn1, "")
	if len(ftHosts) <= 0 {
		logger.Warning(fmt.Sprint("WARNING: Returning AbstractChannel:channelReconnect - There are no FT host URLs configured for this channel"))
		return false
	}

	// This is needed here to avoid a FD leak
	// Execute Derived channel's method - Ignore the Error Handling
	_ = obj.CloseSocket()

	oldUrl := obj.GetChannelURL()
	cn := utils.GetConfigFromKey(utils.ChannelFTRetryIntervalSeconds)
	logger.Debug(fmt.Sprintf("Inside AbstractChannel:channelReconnect config for ChannelFTRetryIntervalSeconds is '%+v", cn))
	connectInterval := obj.GetProperties().GetPropertyAsInt(cn)
	cn = utils.GetConfigFromKey(utils.ChannelFTRetryCount)
	logger.Debug(fmt.Sprintf("Inside AbstractChannel:channelReconnect config for ChannelFTRetryCount is '%+v", cn))
	retryCount := obj.GetProperties().GetPropertyAsInt(cn)
	logger.Debug(fmt.Sprintf("Inside AbstractChannel:channelReconnect Retrying to reconnnect %d times at interval of %d seconds to FTUrls.", retryCount, connectInterval))

	obj.SetChannelLinkState(types.LinkReconnecting)

	logger.Debug(fmt.Sprint("Inside AbstractChannel:channelReconnect about to obj.channelTryRepeatConnect()"))
	err := channelTryRepeatConnect(obj, true)
	if err != nil {
		obj.SetChannelURL(oldUrl.(*LinkUrl))
		obj.SetChannelLinkState(types.LinkClosed)
		logger.Error(fmt.Sprintf("ERROR: Returning AbstractChannel:channelReconnect - failed to reconnect w/ error: /%+v'", err.Error()))
		return false
	}
	obj.SetChannelLinkState(types.LinkConnected)

	logger.Log(fmt.Sprint("Returning AbstractChannel:channelReconnect w/ NO Errors"))
	return true
}

func channelRequestReply(obj types.TGChannel, request types.TGMessage) (types.TGMessage, types.TGError) {
	logger.Log(fmt.Sprint("Entering AbstractChannel:channelRequestReply"))
	var respMessage types.TGMessage

	for {
		logger.Debug(fmt.Sprint("Entering AbstractChannel:channelRequestReply Infinite Loop"))
		resp, err := func() (types.TGMessage, types.TGError) {
			obj.ChannelLock()
			defer obj.ChannelUnlock()

			logger.Debug(fmt.Sprint("Inside AbstractChannel:channelRequestReply Infinite Loop about to obj.Send()"))
			// Execute Derived channel's method
			err := obj.Send(request)
			if err != nil {
				logger.Error(fmt.Sprintf("ERROR: AbstractChannel:channelRequestReply obj.Send failed w/ '%+v'", err.Error()))
				ehResult := channelHandleException(obj, err, true)
				if ehResult.ExceptionType == RethrowException {
					logger.Error(fmt.Sprint("ERROR: Returning AbstractChannel:channelRequestReply - Failed to send message"))
					if err.GetErrorType() == types.TGErrorGeneralException {
						return nil, exception.NewTGGeneralExceptionWithMsg(err.Error())
					}
					errMsg := fmt.Sprintf("AbstractChannel:channelRequestReply - %s w/ error: %s", types.TGDB_SEND_ERROR, err.Error())
					return nil, exception.BuildException(types.TGTransactionStatus(err.GetErrorType()), errMsg)
				} else if ehResult.ExceptionType == Disconnected {
					logger.Error(fmt.Sprint("Returning AbstractChannel:channelRequestReply - channel got disconnected"))
					return nil, exception.NewTGChannelDisconnected(err.GetErrorCode(), err.GetErrorType(), err.GetErrorMsg(), err.GetErrorDetails())
				} else {
					// TODO: Revisit later - Should we not throw an error?
					logger.Warning(fmt.Sprintf("WARNING: Inside AbstractChannel:channelRequestReply in obj.Send retrying to send message on url: '%s'", obj.GetChannelURL().GetUrlAsString()))
					//continue
					return nil, nil
				}
			}

			logger.Debug(fmt.Sprint("Inside AbstractChannel:channelRequestReply Infinite Loop about to obj.ReadWireMsg()"))
			// Execute Derived channel's method
			msg, err := obj.ReadWireMsg()
			if err != nil {
				logger.Error(fmt.Sprintf("ERROR: AbstractChannel:channelRequestReply obj.ReadWireMsg failed w/ '%+v'", err.Error()))
				ehResult := channelHandleException(obj, err, true)
				if ehResult.ExceptionType == RethrowException {
					logger.Error(fmt.Sprint("ERROR: Returning AbstractChannel:channelRequestReply - Failed to read message"))
					if err.GetErrorType() == types.TGErrorGeneralException {
						return nil, exception.NewTGGeneralExceptionWithMsg(err.Error())
					}
					errMsg := fmt.Sprintf("AbstractChannel:channelRequestReply - %s w/ error: %s", types.TGDB_SEND_ERROR, err.Error())
					return nil, exception.BuildException(types.TGTransactionStatus(err.GetErrorType()), errMsg)
				} else if ehResult.ExceptionType == Disconnected {
					logger.Error(fmt.Sprint("Returning AbstractChannel:channelRequestReply - channel got disconnected"))
					return nil, exception.NewTGChannelDisconnected(err.GetErrorCode(), err.GetErrorType(), err.GetErrorMsg(), err.GetErrorDetails())
				} else {
					// TODO: Revisit later - Should we not throw an error?
					logger.Warning(fmt.Sprintf("WARNING: Inside AbstractChannel:channelRequestReply in obj.ReadWireMsg retrying to send message on url: '%s'", obj.GetChannelURL().GetUrlAsString()))
					//continue
					return nil, nil
				}
			}

			//obj.ChannelUnlock()
			return msg, nil
		} ()
		if resp == nil && err == nil {
			continue
		} else if err != nil {
			if err.GetErrorType() == types.TGSuccess {
				respMessage = nil
				break
			}
			return nil, err
		} else {
			logger.Log(fmt.Sprintf("Returning AbstractChannel:channelRequestReply Breaking Loop successfully w/ msgResponse: '%+v'", resp))
			respMessage = resp
			break
		}
	} // End of Infinite Loop

	logger.Log(fmt.Sprintf("Returning AbstractChannel:channelRequestReply w/ %+v", respMessage))
	return respMessage, nil
}

func channelSendMessage(obj types.TGChannel, msg types.TGMessage, resendFlag bool) types.TGError {
	logger.Log(fmt.Sprintf("Entering AbstractChannel:channelSendMessage w/ Message type: '%+v'", msg.GetVerbId()))
	var error types.TGError
	var resendMode types.ResendMode
	if resendFlag {
		resendMode = types.ModeReconnectAndResend
	} else {
		resendMode = types.ModeReconnectAndRaiseException
	}
	logger.Debug(fmt.Sprintf("Inside AbstractChannel:channelSendMessage using '%s'", resendMode.String()))

	for {
		logger.Debug(fmt.Sprint("Entering AbstractChannel:channelSendMessage Infinite Loop"))
		contFlag, err := func() (bool, types.TGError) {
			obj.ChannelLock()
			defer obj.ChannelUnlock()

			if !isChannelConnected(obj) {
				logger.Error(fmt.Sprint("ERROR: Returning AbstractChannel:channelSendMessage - channel is closed"))
				errMsg := fmt.Sprint("AbstractChannel:channelSendMessage - channel is closed")
				return false, exception.GetErrorByType(types.TGErrorGeneralException, types.TGDB_CHANNEL_ERROR, errMsg, "")
			}
			obj.ChannelLock()

			logger.Debug(fmt.Sprint("Inside AbstractChannel:channelSendMessage Infinite Loop about to obj.Send()"))
			// Execute Derived channel's message communication mechanism
			err := obj.Send(msg)
			if err != nil {
				logger.Error(fmt.Sprintf("ERROR: AbstractChannel:channelSendMessage obj.Send failed w/ '%+v'", err.Error()))
				ehResult := channelHandleException(obj, err, false)
				if ehResult.ExceptionType == RethrowException {
					logger.Error(fmt.Sprint("ERROR: Returning AbstractChannel:channelSendMessage - Failed to send message"))
					if err.GetErrorType() == types.TGErrorGeneralException {
						return false, exception.NewTGGeneralExceptionWithMsg(err.Error())
					}
					errMsg := fmt.Sprintf("AbstractChannel:channelSendMessage - %s w/ error: %s", types.TGDB_SEND_ERROR, err.Error())
					return false, exception.BuildException(types.TGTransactionStatus(err.GetErrorType()), errMsg)
				} else if ehResult.ExceptionType == Disconnected {
					logger.Error(fmt.Sprint("ERROR: Returning AbstractChannel:channelSendMessage - channel got disconnected"))
					return false, exception.NewTGChannelDisconnected(err.GetErrorCode(), err.GetErrorType(), err.GetErrorMsg(), err.GetErrorDetails())
				} else {
					// TODO: Revisit later - Should we not throw an error?
					logger.Warning(fmt.Sprintf("WARNING: AbstractChannel:channelSendMessage Retrying to send message on url: '%s'", obj.GetChannelURL().GetUrlAsString()))
					//continue
					return true, nil
				}
			}
			return false, nil
		} ()
		if contFlag {
			continue
		} else {
			logger.Log(fmt.Sprintf("Returning AbstractChannel:channelSendMessage Breaking Loop successfully after sending the message"))
			error = err
			break
		}
	} // End of Infinite Loop
	logger.Log(fmt.Sprintf("Returning AbstractChannel:channelSendMessage"))
	return error
}

func channelSendRequest(obj types.TGChannel, msg types.TGMessage, channelResponse types.TGChannelResponse, resendFlag bool) (types.TGMessage, types.TGError) {
	logger.Log(fmt.Sprintf("Entering AbstractChannel:channelSendRequest w/ Message type: '%+v' ChannelResponse: '%+v'", msg.GetVerbId(), channelResponse))
	reqId := channelResponse.GetRequestId()
	msg.SetRequestId(reqId)

	var respMessage types.TGMessage
	var resendMode types.ResendMode
	if resendFlag {
		resendMode = types.ModeReconnectAndResend
	} else {
		resendMode = types.ModeReconnectAndRaiseException
	}
	logger.Debug(fmt.Sprintf("Inside AbstractChannel:channelSendRequest using '%s'", resendMode.String()))

	for {
		logger.Debug(fmt.Sprintf("Inside AbstractChannel:channelSendRequest Infinite Loop"))
		resp, err := func() (types.TGMessage, types.TGError)  {
			obj.ChannelLock()
			defer obj.ChannelUnlock()

			if !isChannelConnected(obj) {
				errMsg := fmt.Sprintf("AbstractChannel:channelSendRequest - channel is closed")
				logger.Error(fmt.Sprintf("ERROR: Returning %s", errMsg))
				return nil, exception.GetErrorByType(types.TGErrorGeneralException, types.TGDB_CHANNEL_ERROR, errMsg, "")
			}
			// TODO: Uncomment once Trace functionality is implemented and tested
			//if obj.GetTracer() != nil {
			//	obj.GetTracer().Trace(msg)
			//}
			//obj.ChannelLock()
			logger.Debug(fmt.Sprintf("Inside AbstractChannel:channelSendRequest about to set channel response '%+v' in map '%+v'", channelResponse, obj.GetResponses()))
			obj.SetResponse(reqId, channelResponse)

			logger.Debug(fmt.Sprint("Inside AbstractChannel:channelSendRequest Infinite Loop about to obj.Send()"))
			// Execute Derived channel's message communication mechanism
			err := obj.Send(msg)
			logger.Debug(fmt.Sprint("Inside AbstractChannel:channelSendRequest after obj.Send()"))
			if err != nil {
				logger.Error(fmt.Sprintf("ERROR: AbstractChannel:channelSendRequest obj.Send failed w/ '%+v'", err.Error()))
				//obj.ChannelUnlock()
				ehResult := channelHandleException(obj, err, false)
				if ehResult.ExceptionType == RethrowException {
					logger.Error(fmt.Sprint("ERROR: Returning AbstractChannel:channelSendRequest - Failed to send message"))
					if err.GetErrorType() == types.TGErrorGeneralException {
						return nil, exception.NewTGGeneralExceptionWithMsg(err.Error())
					}
					errMsg := fmt.Sprintf("AbstractChannel:channelSendRequest - %s w/ error: %s", types.TGDB_SEND_ERROR, err.Error())
					return nil, exception.BuildException(types.TGTransactionStatus(err.GetErrorType()), errMsg)
				} else if ehResult.ExceptionType == Disconnected {
					logger.Error(fmt.Sprint("Returning AbstractChannel:channelSendRequest - channel got disconnected"))
					return nil, exception.NewTGChannelDisconnected(err.GetErrorCode(), err.GetErrorType(), err.GetErrorMsg(), err.GetErrorDetails())
				} else {
					// TODO: Revisit later - Should we not throw an error?
					logger.Warning(fmt.Sprintf("WARNING: Inside AbstractChannel:channelSendRequest Infinite Loop retrying to send message on url: '%s'", obj.GetChannelURL().GetUrlAsString()))
					//continue
					return nil, nil
				}
			}
			if !channelResponse.IsBlocking() {
				//obj.ChannelUnlock()
				logger.Warning(fmt.Sprint("WARNING: Returning AbstractChannel:channelSendRequest as channel response is NOT blocking"))
				//return nil, nil
				return nil, exception.NewTGSuccessWithMsg("WARNING: Returning AbstractChannel:channelSendRequest as channel response is NOT blocking")
			}
			logger.Debug(fmt.Sprint("Inside AbstractChannel:channelSendRequest Infinite Loop about to channelResponse.Await()"))
			channelResponse.Await(channelResponse.(*BlockingChannelResponse))
			delete(obj.GetResponses(), reqId)
			logger.Debug(fmt.Sprintf("Inside AbstractChannel:channelSendRequest Infinite Loop about to channelResponse.GetReply()"))
			msgResponse := channelResponse.GetReply()

			if msgResponse != nil && msgResponse.GetVerbId() == pdu.VerbExceptionMessage {
				//obj.ChannelUnlock()
				exMsg := msgResponse.(*pdu.ExceptionMessage)
				if exMsg.GetExceptionType() == types.TGErrorRetryIOException {
					//continue
					return nil, nil
				}
				logger.Error(fmt.Sprintf("ERROR: Returning AbstractChannel:channelSendRequest Breaking Loop for VerbExceptionMessage w/ msgRespbnse: '%+v'", msgResponse.String()))
				return nil, exception.NewTGGeneralExceptionWithMsg(exMsg.GetExceptionMsg())
			}
			//obj.ChannelUnlock()
			return msgResponse, nil
		} ()
		if resp == nil && err == nil {
			continue
		} else if err != nil {
			if err.GetErrorType() == types.TGSuccess {
				respMessage = nil
				break
			}
			return nil, err
		} else {
			logger.Log(fmt.Sprintf("Returning AbstractChannel:channelSendRequest Breaking Loop successfully w/ msgResponse: '%+v'", resp))
			respMessage = resp
			break
		}
	} // End of Infinite Loop
	logger.Log(fmt.Sprintf("Returning AbstractChannel:channelSendRequest w/ %+v", respMessage))
	return respMessage, nil
}

func channelStart(obj types.TGChannel) types.TGError {
	logger.Log(fmt.Sprint("Entering AbstractChannel:channelStart"))
	if !isChannelConnected(obj) {
		errMsg := fmt.Sprint("AbstractChannel:channelStart - channel is not connected")
		logger.Error(fmt.Sprintf("ERROR: Returning %s", errMsg))
		return exception.GetErrorByType(types.TGErrorGeneralException, types.TGDB_CHANNEL_ERROR, errMsg, "")
	}
	obj.EnablePing()
	logger.Debug(fmt.Sprint("Inside AbstractChannel:channelStart about to start channel Reader"))
	go obj.GetReader().Start()
	// TODO: Uncomment once Trace functionality is implemented and tested
	//logger.Debug(fmt.Sprint("Inside AbstractChannel:channelStart about to start channel Tracer"))
	//go obj.GetTracer().Start()
	logger.Log(fmt.Sprint("Returning AbstractChannel:channelStart"))
	return nil
}

func channelStop(obj types.TGChannel, bForcefully bool) {
	logger.Log(fmt.Sprint("Entering AbstractChannel:channelStop"))
	obj.ChannelLock()
	defer func() {
		if isChannelClosing(obj) {
			obj.SetChannelLinkState(types.LinkClosed)
		}
		// Execute Derived channel's method - Ignore Error Handling
		obj.ChannelUnlock()
	} ()

	if !isChannelConnected(obj) {
		logger.Warning(fmt.Sprint("WARNING: Returning AbstractChannel:channelStop as channel is already disconnected"))
		return
	}

	if bForcefully || obj.GetNoOfConnections() == 0 {
		logger.Debug(fmt.Sprint("Inside AbstractChannel:channelStop stopping channel"))
		obj.DisablePing()
		logger.Debug(fmt.Sprint("Inside AbstractChannel:channelStop about to stop channel Reader"))
		obj.GetReader().Stop()
		// TODO: Uncomment once Trace functionality is implemented and tested
		//logger.Debug(fmt.Sprint("Inside AbstractChannel:channelStop about to stop channel Tracer"))
		//obj.GetTracer().Stop()

		logger.Debug(fmt.Sprint("Inside AbstractChannel:channelStop about to CreateMessageForVerb()"))
		// Send the disconnect request. sendRequest will not receive a channel response since the channel will be disconnected.
		msgRequest, err := pdu.CreateMessageForVerb(pdu.VerbDisconnectChannelRequest)
		if err != nil {
			logger.Error(fmt.Sprintf("ERROR: Inside AbstractChannel:channelStop VerbDisconnectChannelRequest CreateMessageForVerb failed with '%s'", err.Error()))
			// Execute Derived channel's method - Ignore Error Handling
			_ = obj.CloseSocket()
			return
		}
		// Execute Derived channel's method
		err = obj.Send(msgRequest)
		if err != nil {
			logger.Error(fmt.Sprintf("ERROR: Inside AbstractChannel:channelStop VerbDisconnectChannelRequest send failed with '%s'", err.Error()))
			// Execute Derived channel's method - Ignore Error Handling
			_ = obj.CloseSocket()
			return
		}
		obj.SetChannelLinkState(types.LinkClosing)

		logger.Debug(fmt.Sprint("Inside AbstractChannel:channelStop about to CloseSocket()"))
		// Execute Derived channel's method - Ignore Error Handling
		_ = obj.CloseSocket()
	}
	logger.Log(fmt.Sprint("Returning AbstractChannel:channelStop"))
	return
}

// channelTerminated closes the socket channel. This is called from the ChannelReader.
func channelTerminated(obj types.TGChannel, killMsg string) {
	logger.Log(fmt.Sprint("Entering AbstractChannel:channelTerminated"))
	obj.ExceptionLock()
	defer obj.ExceptionUnlock()

	logger.Debug(fmt.Sprintf("Inside AbstractChannel:channelTerminated about to terminate session/channel with '%s'", killMsg))
	obj.SetChannelLinkState(types.LinkTerminated)

	logger.Debug(fmt.Sprint("Inside AbstractChannel:channelTerminated about to CloseSocket()"))
	// Execute Derived channel's method - Ignore Error Handling
	_ = obj.CloseSocket()
	logger.Log(fmt.Sprintf("Returning AbstractChannel:channelTerminated w/ '%s'", killMsg))
	return
}

func channelTryRepeatConnect(obj types.TGChannel, sleepOnFirstInvocation bool) types.TGError {
	logger.Log(fmt.Sprint("Entering AbstractChannel:channelTryRepeatConnect"))
	cn := utils.GetConfigFromKey(utils.ChannelFTRetryIntervalSeconds)
	//logger.Debug(fmt.Sprintf("Inside AbstractChannel:channelTryRepeatConnect config for ChannelFTRetryIntervalSeconds is '%+v", cn))
	connectInterval := obj.GetProperties().GetPropertyAsInt(cn)
	cn = utils.GetConfigFromKey(utils.ChannelFTRetryCount)
	//logger.Debug(fmt.Sprintf("Inside AbstractChannel:channelTryRepeatConnect config for ChannelFTRetryCount is '%+v", cn))
	retryCount := obj.GetProperties().GetPropertyAsInt(cn)
	logger.Debug(fmt.Sprintf("Inside AbstractChannel:channelTryRepeatConnect Trying to connnect %d times at interval of %d seconds to FTUrls", retryCount, connectInterval))

	reconnected := false
	ftUrls := obj.GetPrimaryURL().GetFTUrls()
	urlCount := len(ftUrls)
	index := obj.GetConnectionIndex()
	logger.Debug(fmt.Sprintf("Inside AbstractChannel:channelTryRepeatConnect current object's primary url '%s' has FTUrls as '%+v'", obj.GetPrimaryURL().GetUrlAsString(), ftUrls))

	for {
		if urlCount > 0 {
			url := ftUrls[index]
			obj.SetChannelURL(url.(*LinkUrl))
		}
		// From here onwards, object's primary attributes will be used such as PrimaryUrl, LinkUrl etc.
		urlStr := obj.GetPrimaryURL().GetUrlAsString()
		logger.Debug(fmt.Sprintf("Inside AbstractChannel:channelTryRepeatConnect Infinite Loop to create a socket for URL: '%s'", urlStr))

		for i := 0; i < retryCount; i++ {
			logger.Debug(fmt.Sprintf("Inside AbstractChannel:channelTryRepeatConnect Attempt:%d to connect to URL:%s", i, urlStr))
			if sleepOnFirstInvocation {
				time.Sleep(time.Duration(connectInterval) * time.Second)
				sleepOnFirstInvocation = false
			}

			logger.Debug(fmt.Sprintf("Inside AbstractChannel:channelTryRepeatConnect about to CreateSocket() on attempt:%d to URL:%s", i, urlStr))
			// Execute Derived channel's method
			err := obj.CreateSocket()
			if err != nil {
				logger.Warning(fmt.Sprintf("WARNING: Inside AbstractChannel:channelTryRepeatConnect about to CloseSocket() on attempt:%d to URL:%s w/ '%+v'", i, urlStr, err.Error()))
				// Execute Derived channel's method - Ignore Error Handling
				_ = obj.CloseSocket()
				continue
			}

			logger.Debug(fmt.Sprintf("Inside AbstractChannel:channelTryRepeatConnect about to OnConnect() on attempt:%d to URL:%s", i, urlStr))
			// Execute Derived channel's method
			err = obj.OnConnect()
			if err != nil {
				logger.Warning(fmt.Sprintf("WARNING: Inside AbstractChannel:channelTryRepeatConnect Failed to execute channel specific OnConnect w/ '%+v'", err.Error()))
				// Execute Derived channel's method - Ignore Error Handling
				_ = obj.CloseSocket()
				continue
			}
			obj.SetConnectionIndex(index)
			logger.Debug(fmt.Sprintf("Inside AbstractChannel:channelTryRepeatConnect successfully created socket and executed OnConnect() on attempt:%d to URL:%s", i, urlStr))
			reconnected = true
			break
		} // End of for loop for Retry Attempts

		if urlCount > 0 {
			index = (index + 1) % urlCount
		} else {
			index += 1
		}

		if index != obj.GetConnectionIndex() || reconnected {
			logger.Debug(fmt.Sprintf("Inside AbstractChannel:channelTryRepeatConnect breaking from Infinite Loop"))
			break
		}
	} // End of Outer Infinite For loop

	if !reconnected {
		errMsg := fmt.Sprintf("AbstractChannel:channelTryRepeatConnect %s - failed %d attempts to connect to TGDB Server.", "TGDB-CONNECT-ERR", retryCount)
		logger.Error(fmt.Sprintf("ERROR: Returning '%s'", errMsg))
		return exception.NewTGConnectionTimeoutWithMsg(errMsg)
	}
	logger.Log(fmt.Sprint("Returning AbstractChannel:channelTryRepeatConnect w/ NO error after successfully creating socket and executing OnConnect()"))
	return nil
}

/////////////////////////////////////////////////////////////////
// Implement functions from Interface ==> TGChannel
/////////////////////////////////////////////////////////////////

// ChannelLock locks the communication channel between TGDB client and server
func (obj *AbstractChannel) ChannelLock() {
	obj.sendLock.Lock()
}

// ChannelUnlock unlocks the communication channel between TGDB client and server
func (obj *AbstractChannel) ChannelUnlock() {
	obj.sendLock.Unlock()
}

// Connect connects the underlying channel using the URL end point
func (obj *AbstractChannel) Connect() types.TGError {
	return channelConnect(obj)
}

// DisablePing disables the pinging ability to the channel
func (obj *AbstractChannel) DisablePing() {
	obj.needsPing = false
}

// Disconnect disconnects the channel from its URL end point
func (obj *AbstractChannel) Disconnect() types.TGError {
	return channelDisConnect(obj)
}

// EnablePing enables the pinging ability to the channel
func (obj *AbstractChannel) EnablePing() {
	obj.needsPing = true
}

// ExceptionLock locks the communication channel between TGDB client and server in case of business exceptions
func (obj *AbstractChannel) ExceptionLock() {
	obj.exceptionLock.Lock()
}

// ExceptionUnlock unlocks the communication channel between TGDB client and server in case of business exceptions
func (obj *AbstractChannel) ExceptionUnlock() {
	obj.exceptionLock.Unlock()
}

// GetAuthToken gets Authorization Token
func (obj *AbstractChannel) GetAuthToken() int64 {
	return obj.authToken
}

// GetClientId gets Client Name
func (obj *AbstractChannel) GetClientId() string {
	return obj.clientId
}

// GetChannelURL gets the channel URL
func (obj *AbstractChannel) GetChannelURL() types.TGChannelUrl {
	return obj.channelUrl
}

// GetConnectionIndex gets the Connection Index
func (obj *AbstractChannel) GetConnectionIndex() int {
	return obj.connectionIndex
}

// GetDataCryptoGrapher gets the data cryptographer handle
func (obj *AbstractChannel) GetDataCryptoGrapher() types.TGDataCryptoGrapher {
	return obj.cryptographer
}

// GetExceptionCondition gets the Exception Condition
func (obj *AbstractChannel) GetExceptionCondition() *sync.Cond {
	return obj.exceptionCond
}

// GetLinkState gets the Link/channel State
func (obj *AbstractChannel) GetLinkState() types.LinkState {
	return obj.channelLinkState
}

// GetNoOfConnections gets number of connections this channel has
func (obj *AbstractChannel) GetNoOfConnections() int32 {
	return obj.numOfConnections
}

// GetPrimaryURL gets the Primary URL
func (obj *AbstractChannel) GetPrimaryURL() types.TGChannelUrl {
	return obj.primaryUrl
}

// GetProperties gets the channel Properties
func (obj *AbstractChannel) GetProperties() types.TGProperties {
	return obj.channelProperties
}

// GetReader gets the channel Reader
func (obj *AbstractChannel) GetReader() types.TGChannelReader {
	return obj.reader
}

// GetResponses gets the channel Response Map
func (obj *AbstractChannel) GetResponses() map[int64]types.TGChannelResponse {
	return obj.responses
}

// GetSessionId gets Session id
func (obj *AbstractChannel) GetSessionId() int64 {
	return obj.sessionId
}

// GetTracer gets the channel Tracer
func (obj *AbstractChannel) GetTracer() types.TGTracer {
	return obj.tracer
}

// IsChannelPingable checks whether the channel is pingable or not
func (obj *AbstractChannel) IsChannelPingable() bool {
	return obj.needsPing
}

// IsClosed checks whether channel is open or closed
func (obj *AbstractChannel) IsClosed() bool {
	return isChannelClosed(obj)
}

// SendMessage sends a Message on this channel, and returns immediately - An Asynchronous or Non-Blocking operation
func (obj *AbstractChannel) SendMessage(msg types.TGMessage) types.TGError {
	return channelSendMessage(obj, msg, true)
}

// SendRequest sends a Message, waits for a response in the message format, and blocks the thread till it gets the response
func (obj *AbstractChannel) SendRequest(msg types.TGMessage, response types.TGChannelResponse) (types.TGMessage, types.TGError) {
	return channelSendRequest(obj, msg, response, true)
}

// SetChannelLinkState sets the Link/channel State
func (obj *AbstractChannel) SetChannelLinkState(state types.LinkState) {
	obj.channelLinkState = state
}

// SetChannelURL sets the channel URL
func (obj *AbstractChannel) SetChannelURL(url types.TGChannelUrl) {
	obj.channelUrl = url.(*LinkUrl)
}

// SetConnectionIndex sets the connection index
func (obj *AbstractChannel) SetConnectionIndex(index int) {
	obj.connectionIndex = index
}

// SetNoOfConnections sets number of connections
func (obj *AbstractChannel) SetNoOfConnections(count int32) {
	obj.numOfConnections = count
}

// SetResponse sets the ChannelResponse Map
func (obj *AbstractChannel) SetResponse(reqId int64, response types.TGChannelResponse) {
	obj.responses[reqId] = response
}

// Start starts the channel so that it can send and receive messages
func (obj *AbstractChannel) Start() types.TGError {
	return channelStart(obj)
}

// Stop stops the channel forcefully or gracefully
func (obj *AbstractChannel) Stop(bForcefully bool) {
	channelStop(obj, bForcefully)
}

// CreateSocket creates a network socket to transfer the messages in the byte format
func (obj *AbstractChannel) CreateSocket() types.TGError {
	logger.Error(fmt.Sprintf("####### ======> ERROR: Entering AbstractChannel:CreateSocket"))
	// No-op for Now! This needs to be implemented by derived channels (TCP/SSL/HTTP)
	return nil
}

// CloseSocket closes the network socket
func (obj *AbstractChannel) CloseSocket() types.TGError {
	logger.Error(fmt.Sprintf("####### ======> ERROR: Entering AbstractChannel:CloseSocket"))
	// No-op for Now! This needs to be implemented by derived channels (TCP/SSL/HTTP)
	return nil
}

// OnConnect executes functional logic after successfully establishing the connection to the server
func (obj *AbstractChannel) OnConnect() types.TGError {
	logger.Error(fmt.Sprintf("####### ======> ERROR: Entering AbstractChannel:OnConnect"))
	// No-op for Now! This needs to be implemented by derived channels (TCP/SSL/HTTP)
	return nil
}

// ReadWireMsg read the message from the network in the byte format
func (obj *AbstractChannel) ReadWireMsg() (types.TGMessage, types.TGError) {
	logger.Error(fmt.Sprintf("####### ======> ERROR: Entering AbstractChannel:ReadWireMsg"))
	// No-op for Now! This needs to be implemented by derived channels (TCP/SSL/HTTP)
	return nil, nil
}

// Send Message to the server, compress and or encrypt.
// Hence it is abstraction, that the channel knows about it.
// @param msg       The message that needs to be sent to the server
func (obj *AbstractChannel) Send(msg types.TGMessage) types.TGError {
	logger.Error(fmt.Sprintf("####### ======> ERROR: Entering AbstractChannel:Send w/ Message as '%s'", msg.String()))
	// No-op for Now! This needs to be implemented by derived channels (TCP/SSL/HTTP)
	return nil
}

func (obj *AbstractChannel) String() string {
	return obj.channelToString()
}
