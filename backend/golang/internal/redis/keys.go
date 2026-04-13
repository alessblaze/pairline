// Pairline - Open Source Video Chat and Matchmaking
// Copyright (C) 2026 Albert Blasczykowski
// Aless Microsystems
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package redis

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	goredis "github.com/redis/go-redis/v9"
)

var ErrSessionRouteNotFound = errors.New("session route not found")
var ErrInvalidSessionRoute = errors.New("invalid session route")

type SessionRoute struct {
	Mode  string
	Shard int
}

func SessionLocatorKey(sessionID string) string {
	return "session:locator:" + sessionID
}

func SessionIPLocatorKey(sessionID string) string {
	return "session:ip_locator:" + sessionID
}

func SessionReportLocatorKey(sessionID string) string {
	return "session:report_locator:" + sessionID
}

func BanIndexKey() string {
	return "bans:index"
}

func BanSessionKey(sessionID string) string {
	return "ban:" + sessionID
}

func BanIPKey(ipAddress string) string {
	return "ban:ip:" + ipAddress
}

func BannedWordsSetKey() string {
	return "moderation:banned_words"
}

func BannedWordsEnabledKey() string {
	return "moderation:banned_words:enabled"
}

func ResolveSessionRoute(ctx context.Context, client goredis.UniversalClient, sessionID string) (SessionRoute, error) {
	locator, err := client.Get(ctx, SessionLocatorKey(sessionID)).Result()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return SessionRoute{}, ErrSessionRouteNotFound
		}
		return SessionRoute{}, err
	}

	return DecodeSessionRoute(locator)
}

func ResolveSessionRouteForReport(ctx context.Context, client goredis.UniversalClient, sessionID string) (SessionRoute, error) {
	route, err := ResolveSessionRoute(ctx, client, sessionID)
	if err == nil || !errors.Is(err, ErrSessionRouteNotFound) {
		return route, err
	}

	locator, fallbackErr := client.Get(ctx, SessionReportLocatorKey(sessionID)).Result()
	if fallbackErr != nil {
		if errors.Is(fallbackErr, goredis.Nil) {
			return SessionRoute{}, ErrSessionRouteNotFound
		}
		return SessionRoute{}, fallbackErr
	}

	return DecodeSessionRoute(locator)
}

func DecodeSessionRoute(locator string) (SessionRoute, error) {
	parts := strings.SplitN(locator, "|", 2)
	if len(parts) != 2 {
		return SessionRoute{}, ErrInvalidSessionRoute
	}

	mode := strings.TrimSpace(parts[0])
	switch mode {
	case "lobby", "text", "video":
	default:
		return SessionRoute{}, ErrInvalidSessionRoute
	}

	shard, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || shard < 0 {
		return SessionRoute{}, ErrInvalidSessionRoute
	}

	return SessionRoute{Mode: mode, Shard: shard}, nil
}

func SessionDataKey(sessionID string, route SessionRoute) string {
	return "session:" + route.Tag() + ":data:" + sessionID
}

func SessionIPKey(sessionID string, route SessionRoute) string {
	return "session:" + route.Tag() + ":ip:" + sessionID
}

func SessionTokenKey(sessionID string, route SessionRoute) string {
	return "session:" + route.Tag() + ":token:" + sessionID
}

func SessionOwnerKey(sessionID string, route SessionRoute) string {
	return "session:" + route.Tag() + ":owner:" + sessionID
}

func MatchKey(sessionID string, route SessionRoute) string {
	return "match:" + route.Tag() + ":" + sessionID
}

func RecentMatchKey(sessionID string, route SessionRoute) string {
	return "recent_match:" + route.Tag() + ":" + sessionID
}

func WebRTCOwnerKey(sessionID string, route SessionRoute) string {
	return "webrtc:" + route.Tag() + ":owner:" + sessionID
}

func WebRTCReadyKey(sessionID string, route SessionRoute) string {
	return "webrtc:" + route.Tag() + ":ready:" + sessionID
}

func WebRTCPendingKey(sessionID string, route SessionRoute) string {
	return "webrtc:" + route.Tag() + ":pending:" + sessionID
}

func (r SessionRoute) Tag() string {
	return fmt.Sprintf("{%s:%d}", r.Mode, r.Shard)
}
