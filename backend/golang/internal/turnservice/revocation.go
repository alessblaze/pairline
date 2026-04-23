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
	for key, allocation := range s.activeAllocations {
		if allocation.Username != userID {
			continue
		}
		allocation.SessionIP = sessionIP
		s.activeAllocations[key] = allocation
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

	s.activeAllocations[key] = activeAllocation{
		Username:  userID,
		SessionIP: s.sessionIPByUserID[userID].IP,
		SrcAddr:   cloneAddr(srcAddr),
		DstAddr:   cloneAddr(dstAddr),
		Protocol:  strings.ToUpper(strings.TrimSpace(protocol)),
	}
}

func (s *Service) untrackActiveAllocation(srcAddr, dstAddr net.Addr, protocol string) {
	key := activeAllocationKey(srcAddr, dstAddr, protocol)
	if key == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	allocation, ok := s.activeAllocations[key]
	delete(s.activeAllocations, key)
	if !ok || allocation.Username == "" {
		return
	}

	for _, remaining := range s.activeAllocations {
		if remaining.Username == allocation.Username {
			return
		}
	}

	delete(s.sessionIPByUserID, allocation.Username)
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
	for _, allocation := range s.activeAllocations {
		if allocation.Username == userID {
			return true
		}
	}
	return false
}

func (s *Service) snapshotActiveAllocations() ([]activeAllocation, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupRememberedSessionIPsLocked(time.Now())

	allocations := make([]activeAllocation, 0, len(s.activeAllocations))
	ips := make([]string, 0, len(s.activeAllocations))
	seenIPs := make(map[string]struct{}, len(s.activeAllocations))

	for _, allocation := range s.activeAllocations {
		allocations = append(allocations, allocation)
		if allocation.SessionIP == "" {
			continue
		}
		if _, ok := seenIPs[allocation.SessionIP]; ok {
			continue
		}
		seenIPs[allocation.SessionIP] = struct{}{}
		ips = append(ips, allocation.SessionIP)
	}

	return allocations, ips
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
	allocations, sessionIPs := s.snapshotActiveAllocations()
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
