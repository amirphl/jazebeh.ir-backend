package scheduler

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/amirphl/Yamata-no-Orochi/models"
)

const (
	splusSendRetryBaseDelay = 1 * time.Second
	splusSendRetryMaxDelay  = 30 * time.Second
)

func splusSendRetryBackoffDelay(attempt int) time.Duration {
	return retryBackoffDelay(attempt, splusSendRetryBaseDelay, splusSendRetryMaxDelay)
}

func (s *SplusCampaignScheduler) sendWithRetry(ctx context.Context, botID string, req *SplusSendMessageRequest) (*SplusResponse, error) {
	var (
		resp *SplusResponse
		err  error
	)
	for attempt := 0; attempt <= splusSendMaxRetries; attempt++ {
		resp, err = s.splusClient.SendMessage(ctx, botID, req)
		if !isRetryableSplusError(resp, err) {
			return resp, err
		}
		if attempt == splusSendMaxRetries {
			break
		}
		if sleepErr := sleepWithContext(ctx, splusSendRetryBackoffDelay(attempt)); sleepErr != nil {
			return resp, ctx.Err()
		}
	}
	return resp, err
}

func (s *SplusCampaignScheduler) resolveSendResult(
	resp *SplusResponse,
	sendErr error,
) (models.SplusSendStatus, int, *string, *string, *string) {
	if sendErr != nil {
		code := "SEND_FAILED"
		desc := sendErr.Error()
		if resp != nil && resp.ResultCode != 0 {
			code = strconv.Itoa(resp.ResultCode)
			if strings.TrimSpace(resp.ResultMessage) != "" {
				desc = resp.ResultMessage
			}
		}
		return models.SplusSendStatusUnsuccessful, 0, nil, &code, &desc
	}

	if resp == nil {
		code := "EMPTY_RESPONSE"
		desc := "empty response from splus"
		return models.SplusSendStatusUnsuccessful, 0, nil, &code, &desc
	}

	if resp.ResultCode == 200 || resp.ResultCode == 202 {
		var serverID *string
		if resp.MessageID != nil && strings.TrimSpace(*resp.MessageID) != "" {
			id := strings.TrimSpace(*resp.MessageID)
			serverID = &id
		} else if resp.RequestID != nil {
			id := strconv.FormatInt(*resp.RequestID, 10)
			serverID = &id
		}
		return models.SplusSendStatusSuccessful, 1, serverID, nil, nil
	}

	code := strconv.Itoa(resp.ResultCode)
	desc := strings.TrimSpace(resp.ResultMessage)
	if desc == "" {
		if msg, ok := splusErrorDescriptions[resp.ResultCode]; ok {
			desc = msg
		}
	}
	return models.SplusSendStatusUnsuccessful, 0, nil, &code, &desc
}

func isRetryableSplusError(resp *SplusResponse, err error) bool {
	if err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return true
		}
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "429") ||
			strings.Contains(msg, "too many requests") ||
			strings.Contains(msg, "timeout") ||
			strings.Contains(msg, "timed out") ||
			strings.Contains(msg, "connection reset") ||
			strings.Contains(msg, "connection refused") ||
			strings.Contains(msg, "connection aborted") ||
			strings.Contains(msg, "temporary failure") ||
			strings.Contains(msg, "no such host") ||
			strings.Contains(msg, "eof") {
			return true
		}
	}

	if resp == nil {
		return false
	}

	if resp.HTTPStatus == http.StatusTooManyRequests {
		return true
	}
	if resp.HTTPStatus >= 500 && resp.HTTPStatus <= 599 {
		return true
	}

	switch resp.ResultCode {
	case 429, 500, 724, 730, 736, 738:
		return true
	default:
		return false
	}
}

var splusErrorDescriptions = map[int]string{
	400: "Bad Request",
	401: "Unauthorized",
	404: "Not Found",
	429: "Too Many Requests",
	470: "Input Validation Error",
	500: "Internal Server Error",
	700: "Invalid Phone Number",
	701: "No User Account",
	702: "Suspended User",
	712: "User Is Inactive",
	715: "User Type Not Authorized",
	716: "File Not Found",
	723: "Insufficient Balance",
	724: "Cannot Download File",
	726: "File Invalid",
	727: "File Size Invalid",
	728: "File Name Invalid",
	729: "Mime Type Invalid",
	730: "Cannot Create User",
	731: "No Active Conversation",
	732: "Receiver Not Specified",
	733: "URL Not Allowed",
	734: "APK Not Allowed",
	735: "Sender Is Blocked",
	736: "Server Connection Error",
	738: "Data Persist Error",
	739: "Request Not Allowed",
	740: "File Access Denied",
}
