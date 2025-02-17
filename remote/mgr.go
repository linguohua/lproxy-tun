package remote

import (
	"fmt"
	"lproxy_tun/meta"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
)

type MgrConfig struct {
	WebsocketURL string
	TunnelCount  int
	TunnelCap    int

	Protector func(fd uint64)
}

type Mgr struct {
	config  MgrConfig
	index   atomic.Uint64
	tunnels []*WSTunnel

	isActivated bool

	localGvisor meta.LocalGivsorNetwork
}

func NewMgr(config *MgrConfig) *Mgr {
	cfg := *config
	if len(cfg.WebsocketURL) == 0 {
		cfg.WebsocketURL = "ws://127.0.0.1:8080/ws"
	}

	if cfg.TunnelCount < 1 {
		cfg.TunnelCount = 1
	}

	if cfg.TunnelCap < 1 {
		cfg.TunnelCap = 100
	}

	mgr := &Mgr{
		config: cfg,
	}

	return mgr
}

func (mgr *Mgr) OnStackReady(localGvisor meta.LocalGivsorNetwork) {
	mgr.localGvisor = localGvisor
}

func (mgr *Mgr) Startup() error {
	log.Info("remote.Mgr.Startup called")

	if mgr.isActivated {
		return fmt.Errorf("remote.Mgr already startup")
	}

	config := &mgr.config

	mgr.tunnels = make([]*WSTunnel, 0, config.TunnelCount)
	for i := 0; i < config.TunnelCount; i++ {
		tnl := newTunnel(config.WebsocketURL, config.TunnelCap)
		if config.Protector != nil {
			tnl.protector = config.Protector
		}

		mgr.tunnels = append(mgr.tunnels, tnl)

		tnl.start()
	}

	mgr.isActivated = true

	go mgr.keepalive()

	log.Info("remote.Mgr.Startup completed")
	return nil
}

func (mgr *Mgr) Shutdown() error {
	log.Info("remote.Mgr.Shutdown called")

	if !mgr.isActivated {
		return fmt.Errorf("remote.Mgr isn't startup")
	}

	count := len(mgr.tunnels)
	for i := 0; i < count; i++ {
		tnl := mgr.tunnels[i]
		tnl.stop()
	}

	mgr.isActivated = false

	log.Info("remote.Mgr.Shutdown completed")
	return nil
}

func (mgr *Mgr) keepalive() {
	count := len(mgr.tunnels)

	log.Infof("remote.Mgr keepalive goroutine start, tunnel count:%d", count)

	for mgr.isActivated {
		time.Sleep(time.Second * 5)

		for i := 0; i < count; i++ {
			tnl := mgr.tunnels[i]
			tnl.keepalive()
		}
	}

	log.Info("remote.Mgr keepalive goroutine exit")
}

func (mgr *Mgr) HandleTCP(conn meta.TCPConn) {
	handled := false
	defer func() {
		if !handled {
			conn.Close()
		}
	}()

	// allocate a usable tunnel
	tunnel, err := mgr.allocateWSTunnel()
	if err != nil {
		log.Errorf("mgr.allocateWSTunnel failed: %v", err)
		return
	}

	err = tunnel.acceptTCPConn(conn)
	if err != nil {
		log.Errorf("tunnel.acceptTCPConn failed: %v", err)
		return
	}

	handled = true
}

func (mgr *Mgr) HandleUDP(conn meta.UDPConn) {
	handled := false
	defer func() {
		if !handled {
			conn.Close()
		}
	}()

	// allocate a usable tunnel
	tunnel, err := mgr.allocateWSTunnel()
	if err != nil {
		log.Errorf("mgr.allocateWSTunnel failed: %v", err)
		return
	}

	err = tunnel.acceptUDPConn(conn)
	if err != nil {
		log.Errorf("tunnel.acceptUDPConn failed: %v", err)
		return
	}

	handled = true
}

func (mgr *Mgr) nextAllocIndex() uint64 {
	if len(mgr.tunnels) < 1 {
		return 0
	}

	return mgr.index.Add(1) % uint64(len(mgr.tunnels))
}

func (mgr *Mgr) allocateWSTunnel() (*WSTunnel, error) {
	if len(mgr.tunnels) < 1 {
		return nil, fmt.Errorf("tunnels array is empty")
	}

	index := mgr.nextAllocIndex()
	firstIndex := index

	for {
		tnl := mgr.tunnels[index]
		if tnl.isValid() {
			return tnl, nil
		}

		index = mgr.nextAllocIndex()
		if firstIndex == index {
			break
		}
	}

	return nil, fmt.Errorf("failed to find a valid tunnel")
}
