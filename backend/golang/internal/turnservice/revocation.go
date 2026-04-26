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
	Username           string
	SessionIP          string
	SrcAddr            net.Addr
	DstAddr            net.Addr
	Protocol           string
	ReleaseOperationID string
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

	remembered := s.sessionIPByUserID[userID]
	if previous, ok := s.activeAllocations[key]; ok {
		if strings.TrimSpace(remembered.IP) == "" && strings.TrimSpace(previous.Username) == strings.TrimSpace(userID) {
			remembered = rememberedSessionIP{
				IP:     strings.TrimSpace(previous.SessionIP),
				SeenAt: time.Now(),
			}
		}
		s.removeAllocationIndexesLocked(key, previous)
	}
	if strings.TrimSpace(remembered.IP) != "" {
		s.sessionIPByUserID[userID] = remembered
	}

	allocation := activeAllocation{
		Username:           userID,
		SessionIP:          remembered.IP,
		SrcAddr:            cloneAddr(srcAddr),
		DstAddr:            cloneAddr(dstAddr),
		Protocol:           strings.ToUpper(strings.TrimSpace(protocol)),
		ReleaseOperationID: s.nextReleaseOperationID(),
	}
	if s.allocationReleaseLookup == nil {
		s.allocationReleaseLookup = make(map[string]activeAllocation)
	}
	s.activeAllocations[key] = allocation
	s.allocationReleaseLookup[key] = allocation
	s.addAllocationIndexesLocked(key, allocation)
}

func (s *Service) untrackActiveAllocation(srcAddr, dstAddr net.Addr, protocol string) (activeAllocation, bool) {
	key := activeAllocationKey(srcAddr, dstAddr, protocol)
	if key == "" {
		return activeAllocation{}, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	allocation, ok := s.activeAllocations[key]
	if !ok {
		fallback, ok := s.allocationReleaseLookup[key]
		if !ok {
			return activeAllocation{}, false
		}
		delete(s.allocationReleaseLookup, key)
		return fallback, true
	}
	s.removeAllocationIndexesLocked(key, allocation)
	delete(s.allocationReleaseLookup, key)
	return allocation, true
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

func revokeTurnAllocation(server *pionturn.Server, allocation activeAllocation) (panicErr error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			panicErr = fmt.Errorf("turn allocation revocation panicked: %v", recovered)
		}
	}()

	if server == nil {
		return fmt.Errorf("turn server is not initialized")
	}
	if allocation.SrcAddr == nil || allocation.DstAddr == nil {
		return fmt.Errorf("turn allocation addresses are required")
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
		managerValue := allocationManagerValue(allocationManagersField.Index(i))
		deleteMethod := managerValue.MethodByName("DeleteAllocation")
		if !deleteMethod.IsValid() {
			return fmt.Errorf("turn allocation delete method unavailable")
		}

		fiveTuple, err := buildTurnFiveTupleValue(deleteMethod.Type().In(0), allocation)
		if err != nil {
			return err
		}
		deleteMethod.Call([]reflect.Value{fiveTuple})
	}

	return nil
}

func turnServerHasAllocation(server *pionturn.Server, allocation activeAllocation) (exists bool, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			exists = false
			err = fmt.Errorf("turn allocation inspection panicked: %v", recovered)
		}
	}()

	if server == nil {
		return false, fmt.Errorf("turn server is not initialized")
	}
	if allocation.SrcAddr == nil || allocation.DstAddr == nil {
		return false, fmt.Errorf("turn allocation addresses are required")
	}

	serverValue := reflect.ValueOf(server)
	if serverValue.Kind() != reflect.Ptr || serverValue.IsNil() {
		return false, fmt.Errorf("invalid turn server")
	}

	allocationManagersField := serverValue.Elem().FieldByName("allocationManagers")
	if !allocationManagersField.IsValid() || allocationManagersField.Kind() != reflect.Slice {
		return false, fmt.Errorf("turn server allocation managers unavailable")
	}

	for i := 0; i < allocationManagersField.Len(); i++ {
		managerValue := allocationManagerValue(allocationManagersField.Index(i))
		if managerValue.IsNil() {
			continue
		}

		getMethod := managerValue.MethodByName("GetAllocation")
		if !getMethod.IsValid() {
			return false, fmt.Errorf("turn allocation get method unavailable")
		}

		fiveTuple, err := buildTurnFiveTupleValue(getMethod.Type().In(0), allocation)
		if err != nil {
			return false, err
		}
		results := getMethod.Call([]reflect.Value{fiveTuple})
		if len(results) != 1 {
			return false, fmt.Errorf("turn allocation get signature unavailable")
		}
		if !results[0].IsNil() {
			return true, nil
		}
	}

	return false, nil
}

func allocationManagerValue(value reflect.Value) reflect.Value {
	return reflect.NewAt(
		value.Type(),
		unsafe.Pointer(value.UnsafeAddr()),
	).Elem()
}

func buildTurnFiveTupleValue(fiveTupleType reflect.Type, allocation activeAllocation) (reflect.Value, error) {
	if fiveTupleType.Kind() != reflect.Ptr || fiveTupleType.Elem().Kind() != reflect.Struct {
		return reflect.Value{}, fmt.Errorf("turn allocation five-tuple signature unavailable")
	}

	fiveTuple := reflect.New(fiveTupleType.Elem())
	protocolField := fiveTuple.Elem().FieldByName("Protocol")
	srcAddrField := fiveTuple.Elem().FieldByName("SrcAddr")
	dstAddrField := fiveTuple.Elem().FieldByName("DstAddr")
	if !protocolField.IsValid() || !protocolField.CanSet() ||
		!srcAddrField.IsValid() || !srcAddrField.CanSet() ||
		!dstAddrField.IsValid() || !dstAddrField.CanSet() {
		return reflect.Value{}, fmt.Errorf("turn allocation five-tuple shape unavailable")
	}
	if !reflect.TypeOf(allocation.SrcAddr).AssignableTo(srcAddrField.Type()) ||
		!reflect.TypeOf(allocation.DstAddr).AssignableTo(dstAddrField.Type()) {
		return reflect.Value{}, fmt.Errorf("turn allocation address types are incompatible")
	}

	protocolField.SetUint(uint64(turnAllocationProtocol(allocation.Protocol)))
	srcAddrField.Set(reflect.ValueOf(allocation.SrcAddr))
	dstAddrField.Set(reflect.ValueOf(allocation.DstAddr))
	return fiveTuple, nil
}

func turnAllocationProtocol(protocol string) uint8 {
	switch strings.ToUpper(strings.TrimSpace(protocol)) {
	case "TCP":
		return 1
	default:
		return 0
	}
}
