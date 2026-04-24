package turnservice

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"reflect"
	"strings"
	"time"
	"unsafe"

	pionturn "github.com/pion/turn/v5"
)

const banSweepInterval = 10 * time.Second
const rememberedSessionIPTTL = time.Minute

type activeAllocation struct {
	Username  string
	SessionIP string
	SrcAddr   net.Addr
	DstAddr   net.Addr
	Protocol  string
}

type rememberedSessionIP struct {
	IP     string
	SeenAt time.Time
}

func activeAllocationKey(srcAddr, dstAddr net.Addr, protocol string) string {
	return strings.Join([]string{
		strings.ToUpper(strings.TrimSpace(protocol)),
		addrKey(srcAddr),
		addrKey(dstAddr),
	}, "|")
}

func addrKey(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	return addr.Network() + ":" + addr.String()
}

func cloneAddr(addr net.Addr) net.Addr {
	switch typed := addr.(type) {
	case *net.UDPAddr:
		copied := *typed
		if typed.IP != nil {
			copied.IP = append(net.IP(nil), typed.IP...)
		}
		return &copied
	case *net.TCPAddr:
		copied := *typed
		if typed.IP != nil {
			copied.IP = append(net.IP(nil), typed.IP...)
		}
		return &copied
	default:
		return addr
	}
}

func (s *Service) rememberSessionIP(userID, sessionIP string) {
	sessionIP = strings.TrimSpace(sessionIP)
	if sessionIP == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	s.sessionIPByUserID[userID] = rememberedSessionIP{
		IP:     sessionIP,
		SeenAt: now,
	}
	for key := range s.allocationKeysByUserID[userID] {
		allocation := s.activeAllocations[key]
		if previousSessionIP := strings.TrimSpace(allocation.SessionIP); previousSessionIP != "" && previousSessionIP != sessionIP {
			s.removeAllocationKeyFromSessionIPLocked(previousSessionIP, key)
		}
		allocation.SessionIP = sessionIP
		s.activeAllocations[key] = allocation
		s.addAllocationKeyToSessionIPLocked(sessionIP, key)
	}
	s.cleanupRememberedSessionIPsLocked(now)
}

func (s *Service) trackActiveAllocation(srcAddr, dstAddr net.Addr, protocol, userID string) {
	key := activeAllocationKey(srcAddr, dstAddr, protocol)
	if key == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if previous, ok := s.activeAllocations[key]; ok {
		s.removeAllocationIndexesLocked(key, previous)
	}

	allocation := activeAllocation{
		Username:  userID,
		SessionIP: s.sessionIPByUserID[userID].IP,
		SrcAddr:   cloneAddr(srcAddr),
		DstAddr:   cloneAddr(dstAddr),
		Protocol:  strings.ToUpper(strings.TrimSpace(protocol)),
	}
	s.activeAllocations[key] = allocation
	s.addAllocationIndexesLocked(key, allocation)
}

func (s *Service) untrackActiveAllocation(srcAddr, dstAddr net.Addr, protocol string) {
	key := activeAllocationKey(srcAddr, dstAddr, protocol)
	if key == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	allocation, ok := s.activeAllocations[key]
	if !ok {
		return
	}
	s.removeAllocationIndexesLocked(key, allocation)
}

func (s *Service) cleanupRememberedSessionIPsLocked(now time.Time) {
	for userID, remembered := range s.sessionIPByUserID {
		if now.Sub(remembered.SeenAt) < rememberedSessionIPTTL {
			continue
		}
		if s.hasActiveAllocationForUsernameLocked(userID) {
			continue
		}
		delete(s.sessionIPByUserID, userID)
	}
}

func (s *Service) hasActiveAllocationForUsernameLocked(userID string) bool {
	return s.allocationCountByUserID[userID] > 0
}

func (s *Service) snapshotSessionIPs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupRememberedSessionIPsLocked(time.Now())

	ips := make([]string, 0, len(s.allocationKeysBySession))
	for sessionIP := range s.allocationKeysBySession {
		if strings.TrimSpace(sessionIP) == "" {
			continue
		}
		ips = append(ips, sessionIP)
	}

	return ips
}

func (s *Service) snapshotAllocationsForSessionIPs(sessionIPs []string) []activeAllocation {
	s.mu.Lock()
	defer s.mu.Unlock()

	allocations := make([]activeAllocation, 0, len(sessionIPs))
	seenAllocationKeys := make(map[string]struct{})
	for _, sessionIP := range sessionIPs {
		for key := range s.allocationKeysBySession[strings.TrimSpace(sessionIP)] {
			if _, ok := seenAllocationKeys[key]; ok {
				continue
			}
			allocation, ok := s.activeAllocations[key]
			if !ok {
				continue
			}
			seenAllocationKeys[key] = struct{}{}
			allocations = append(allocations, allocation)
		}
	}

	return allocations
}

func (s *Service) runBanSweep(ctx context.Context) {
	ticker := time.NewTicker(banSweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweepCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := s.revokeBannedAllocations(sweepCtx)
			cancel()
			if err != nil {
				log.Printf("TURN ban sweep failed: %v", err)
			}
		}
	}
}

func (s *Service) revokeBannedAllocations(ctx context.Context) error {
	sessionIPs := s.snapshotSessionIPs()
	if len(sessionIPs) == 0 {
		return nil
	}

	bannedIPs, err := s.validator.CheckBannedSessionIPs(ctx, sessionIPs)
	if err != nil {
		return err
	}
	if len(bannedIPs) == 0 {
		return nil
	}

	bannedSet := make(map[string]struct{}, len(bannedIPs))
	for _, bannedIP := range bannedIPs {
		bannedSet[strings.TrimSpace(bannedIP)] = struct{}{}
	}

	allocations := s.snapshotAllocationsForSessionIPs(bannedIPs)
	var revokeErr error
	for _, allocation := range allocations {
		if _, ok := bannedSet[allocation.SessionIP]; !ok {
			continue
		}
		if err := s.revokeAllocation(allocation); err != nil {
			revokeErr = errors.Join(revokeErr, err)
			continue
		}
		log.Printf("TURN allocation revoked session_ip=%s peer_addr=%v", allocation.SessionIP, allocation.SrcAddr)
	}

	return revokeErr
}

func (s *Service) addAllocationIndexesLocked(key string, allocation activeAllocation) {
	username := strings.TrimSpace(allocation.Username)
	if username != "" {
		if s.allocationKeysByUserID[username] == nil {
			s.allocationKeysByUserID[username] = make(map[string]struct{})
		}
		s.allocationKeysByUserID[username][key] = struct{}{}
		s.allocationCountByUserID[username]++
	}
	s.addAllocationKeyToSessionIPLocked(allocation.SessionIP, key)
}

func (s *Service) removeAllocationIndexesLocked(key string, allocation activeAllocation) {
	delete(s.activeAllocations, key)

	username := strings.TrimSpace(allocation.Username)
	if username != "" {
		if keys := s.allocationKeysByUserID[username]; keys != nil {
			delete(keys, key)
			if len(keys) == 0 {
				delete(s.allocationKeysByUserID, username)
			}
		}
		if remaining := s.allocationCountByUserID[username] - 1; remaining > 0 {
			s.allocationCountByUserID[username] = remaining
		} else {
			delete(s.allocationCountByUserID, username)
			delete(s.sessionIPByUserID, username)
		}
	}

	s.removeAllocationKeyFromSessionIPLocked(allocation.SessionIP, key)
}

func (s *Service) addAllocationKeyToSessionIPLocked(sessionIP, key string) {
	sessionIP = strings.TrimSpace(sessionIP)
	if sessionIP == "" {
		return
	}
	if s.allocationKeysBySession[sessionIP] == nil {
		s.allocationKeysBySession[sessionIP] = make(map[string]struct{})
	}
	s.allocationKeysBySession[sessionIP][key] = struct{}{}
}

func (s *Service) removeAllocationKeyFromSessionIPLocked(sessionIP, key string) {
	sessionIP = strings.TrimSpace(sessionIP)
	if sessionIP == "" {
		return
	}
	keys := s.allocationKeysBySession[sessionIP]
	if keys == nil {
		return
	}
	delete(keys, key)
	if len(keys) == 0 {
		delete(s.allocationKeysBySession, sessionIP)
	}
}

func revokeTurnAllocation(server *pionturn.Server, allocation activeAllocation) error {
	if server == nil {
		return fmt.Errorf("turn server is not initialized")
	}

	serverValue := reflect.ValueOf(server)
	if serverValue.Kind() != reflect.Ptr || serverValue.IsNil() {
		return fmt.Errorf("invalid turn server")
	}

	allocationManagersField := serverValue.Elem().FieldByName("allocationManagers")
	if !allocationManagersField.IsValid() || allocationManagersField.Kind() != reflect.Slice {
		return fmt.Errorf("turn server allocation managers unavailable")
	}

	for i := 0; i < allocationManagersField.Len(); i++ {
		managerValue := reflect.NewAt(
			allocationManagersField.Index(i).Type(),
			unsafe.Pointer(allocationManagersField.Index(i).UnsafeAddr()),
		).Elem()
		deleteMethod := managerValue.MethodByName("DeleteAllocation")
		if !deleteMethod.IsValid() {
			return fmt.Errorf("turn allocation delete method unavailable")
		}

		fiveTupleType := deleteMethod.Type().In(0)
		fiveTuple := reflect.New(fiveTupleType.Elem())
		fiveTuple.Elem().FieldByName("Protocol").SetUint(uint64(turnAllocationProtocol(allocation.Protocol)))
		fiveTuple.Elem().FieldByName("SrcAddr").Set(reflect.ValueOf(allocation.SrcAddr))
		fiveTuple.Elem().FieldByName("DstAddr").Set(reflect.ValueOf(allocation.DstAddr))
		deleteMethod.Call([]reflect.Value{fiveTuple})
	}

	return nil
}

func turnAllocationProtocol(protocol string) uint8 {
	switch strings.ToUpper(strings.TrimSpace(protocol)) {
	case "TCP":
		return 1
	default:
		return 0
	}
}
