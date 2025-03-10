package config

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/armon/go-metrics/prometheus"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/require"

	"github.com/hashicorp/consul/agent/cache"
	"github.com/hashicorp/consul/agent/checks"
	"github.com/hashicorp/consul/agent/consul"
	"github.com/hashicorp/consul/agent/structs"
	"github.com/hashicorp/consul/agent/token"
	"github.com/hashicorp/consul/lib"
	"github.com/hashicorp/consul/logging"
	"github.com/hashicorp/consul/sdk/testutil"
	"github.com/hashicorp/consul/types"
)

type configTest struct {
	desc           string
	args           []string
	pre            func()
	json, jsontail []string
	hcl, hcltail   []string
	privatev4      func() ([]*net.IPAddr, error)
	publicv6       func() ([]*net.IPAddr, error)
	patch          func(rt *RuntimeConfig)
	patchActual    func(rt *RuntimeConfig)
	err            string
	warns          []string
	hostname       func() (string, error)
}

// TestConfigFlagsAndEdgecases tests the command line flags and
// edgecases for the config parsing. It provides a test structure which
// checks for warnings on deprecated fields and flags.  These tests
// should check one option at a time if possible and should use generic
// values, e.g. 'a' or 1 instead of 'servicex' or 3306.

func TestBuilder_BuildAndValidate_ConfigFlagsAndEdgecases(t *testing.T) {
	if testing.Short() {
		t.Skip("too slow for testing.Short")
	}

	dataDir := testutil.TempDir(t, "consul")

	defaultEntMeta := structs.DefaultEnterpriseMeta()

	tests := []configTest{
		// ------------------------------------------------------------
		// cmd line flags
		//

		{
			desc: "-advertise",
			args: []string{
				`-advertise=1.2.3.4`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.AdvertiseAddrLAN = ipAddr("1.2.3.4")
				rt.AdvertiseAddrWAN = ipAddr("1.2.3.4")
				rt.RPCAdvertiseAddr = tcpAddr("1.2.3.4:8300")
				rt.SerfAdvertiseAddrLAN = tcpAddr("1.2.3.4:8301")
				rt.SerfAdvertiseAddrWAN = tcpAddr("1.2.3.4:8302")
				rt.TaggedAddresses = map[string]string{
					"lan":      "1.2.3.4",
					"lan_ipv4": "1.2.3.4",
					"wan":      "1.2.3.4",
					"wan_ipv4": "1.2.3.4",
				}
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-advertise-wan",
			args: []string{
				`-advertise-wan=1.2.3.4`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.AdvertiseAddrWAN = ipAddr("1.2.3.4")
				rt.SerfAdvertiseAddrWAN = tcpAddr("1.2.3.4:8302")
				rt.TaggedAddresses = map[string]string{
					"lan":      "10.0.0.1",
					"lan_ipv4": "10.0.0.1",
					"wan":      "1.2.3.4",
					"wan_ipv4": "1.2.3.4",
				}
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-advertise and -advertise-wan",
			args: []string{
				`-advertise=1.2.3.4`,
				`-advertise-wan=5.6.7.8`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.AdvertiseAddrLAN = ipAddr("1.2.3.4")
				rt.AdvertiseAddrWAN = ipAddr("5.6.7.8")
				rt.RPCAdvertiseAddr = tcpAddr("1.2.3.4:8300")
				rt.SerfAdvertiseAddrLAN = tcpAddr("1.2.3.4:8301")
				rt.SerfAdvertiseAddrWAN = tcpAddr("5.6.7.8:8302")
				rt.TaggedAddresses = map[string]string{
					"lan":      "1.2.3.4",
					"lan_ipv4": "1.2.3.4",
					"wan":      "5.6.7.8",
					"wan_ipv4": "5.6.7.8",
				}
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-bind",
			args: []string{
				`-bind=1.2.3.4`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.BindAddr = ipAddr("1.2.3.4")
				rt.AdvertiseAddrLAN = ipAddr("1.2.3.4")
				rt.AdvertiseAddrWAN = ipAddr("1.2.3.4")
				rt.RPCAdvertiseAddr = tcpAddr("1.2.3.4:8300")
				rt.RPCBindAddr = tcpAddr("1.2.3.4:8300")
				rt.SerfAdvertiseAddrLAN = tcpAddr("1.2.3.4:8301")
				rt.SerfAdvertiseAddrWAN = tcpAddr("1.2.3.4:8302")
				rt.SerfBindAddrLAN = tcpAddr("1.2.3.4:8301")
				rt.SerfBindAddrWAN = tcpAddr("1.2.3.4:8302")
				rt.TaggedAddresses = map[string]string{
					"lan":      "1.2.3.4",
					"lan_ipv4": "1.2.3.4",
					"wan":      "1.2.3.4",
					"wan_ipv4": "1.2.3.4",
				}
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-bootstrap",
			args: []string{
				`-bootstrap`,
				`-server`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.Bootstrap = true
				rt.ServerMode = true
				rt.LeaveOnTerm = false
				rt.SkipLeaveOnInt = true
				rt.DataDir = dataDir
			},
			warns: []string{"bootstrap = true: do not enable unless necessary"},
		},
		{
			desc: "-bootstrap-expect",
			args: []string{
				`-bootstrap-expect=3`,
				`-server`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.BootstrapExpect = 3
				rt.ServerMode = true
				rt.LeaveOnTerm = false
				rt.SkipLeaveOnInt = true
				rt.DataDir = dataDir
			},
			warns: []string{"bootstrap_expect > 0: expecting 3 servers"},
		},
		{
			desc: "-client",
			args: []string{
				`-client=1.2.3.4`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.ClientAddrs = []*net.IPAddr{ipAddr("1.2.3.4")}
				rt.DNSAddrs = []net.Addr{tcpAddr("1.2.3.4:8600"), udpAddr("1.2.3.4:8600")}
				rt.HTTPAddrs = []net.Addr{tcpAddr("1.2.3.4:8500")}
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-config-dir",
			args: []string{
				`-data-dir=` + dataDir,
				`-config-dir`, filepath.Join(dataDir, "conf.d"),
			},
			patch: func(rt *RuntimeConfig) {
				rt.Datacenter = "a"
				rt.ACLDatacenter = "a"
				rt.PrimaryDatacenter = "a"
				rt.DataDir = dataDir
			},
			pre: func() {
				writeFile(filepath.Join(dataDir, "conf.d/conf.json"), []byte(`{"datacenter":"a"}`))
			},
		},
		{
			desc: "-config-file json",
			args: []string{
				`-data-dir=` + dataDir,
				`-config-file`, filepath.Join(dataDir, "conf.json"),
			},
			patch: func(rt *RuntimeConfig) {
				rt.Datacenter = "a"
				rt.ACLDatacenter = "a"
				rt.PrimaryDatacenter = "a"
				rt.DataDir = dataDir
			},
			pre: func() {
				writeFile(filepath.Join(dataDir, "conf.json"), []byte(`{"datacenter":"a"}`))
			},
		},
		{
			desc: "-config-file hcl and json",
			args: []string{
				`-data-dir=` + dataDir,
				`-config-file`, filepath.Join(dataDir, "conf.hcl"),
				`-config-file`, filepath.Join(dataDir, "conf.json"),
			},
			patch: func(rt *RuntimeConfig) {
				rt.Datacenter = "b"
				rt.ACLDatacenter = "b"
				rt.PrimaryDatacenter = "b"
				rt.DataDir = dataDir
			},
			pre: func() {
				writeFile(filepath.Join(dataDir, "conf.hcl"), []byte(`datacenter = "a"`))
				writeFile(filepath.Join(dataDir, "conf.json"), []byte(`{"datacenter":"b"}`))
			},
		},
		{
			desc: "-data-dir empty",
			args: []string{
				`-data-dir=`,
			},
			err: "data_dir cannot be empty",
		},
		{
			desc: "-data-dir non-directory",
			args: []string{
				`-data-dir=runtime_test.go`,
			},
			err: `data_dir "runtime_test.go" is not a directory`,
		},
		{
			desc: "-datacenter",
			args: []string{
				`-datacenter=a`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.Datacenter = "a"
				rt.ACLDatacenter = "a"
				rt.PrimaryDatacenter = "a"
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-datacenter empty",
			args: []string{
				`-datacenter=`,
				`-data-dir=` + dataDir,
			},
			err: "datacenter cannot be empty",
		},
		{
			desc: "-dev",
			args: []string{
				`-dev`,
			},
			patch: func(rt *RuntimeConfig) {
				rt.AdvertiseAddrLAN = ipAddr("127.0.0.1")
				rt.AdvertiseAddrWAN = ipAddr("127.0.0.1")
				rt.BindAddr = ipAddr("127.0.0.1")
				rt.ConnectEnabled = true
				rt.DevMode = true
				rt.DisableAnonymousSignature = true
				rt.DisableKeyringFile = true
				rt.EnableDebug = true
				rt.UIConfig.Enabled = true
				rt.LeaveOnTerm = false
				rt.Logging.LogLevel = "DEBUG"
				rt.RPCAdvertiseAddr = tcpAddr("127.0.0.1:8300")
				rt.RPCBindAddr = tcpAddr("127.0.0.1:8300")
				rt.SerfAdvertiseAddrLAN = tcpAddr("127.0.0.1:8301")
				rt.SerfAdvertiseAddrWAN = tcpAddr("127.0.0.1:8302")
				rt.SerfBindAddrLAN = tcpAddr("127.0.0.1:8301")
				rt.SerfBindAddrWAN = tcpAddr("127.0.0.1:8302")
				rt.ServerMode = true
				rt.SkipLeaveOnInt = true
				rt.TaggedAddresses = map[string]string{
					"lan":      "127.0.0.1",
					"lan_ipv4": "127.0.0.1",
					"wan":      "127.0.0.1",
					"wan_ipv4": "127.0.0.1",
				}
				rt.ConsulCoordinateUpdatePeriod = 100 * time.Millisecond
				rt.ConsulRaftElectionTimeout = 52 * time.Millisecond
				rt.ConsulRaftHeartbeatTimeout = 35 * time.Millisecond
				rt.ConsulRaftLeaderLeaseTimeout = 20 * time.Millisecond
				rt.GossipLANGossipInterval = 100 * time.Millisecond
				rt.GossipLANProbeInterval = 100 * time.Millisecond
				rt.GossipLANProbeTimeout = 100 * time.Millisecond
				rt.GossipLANSuspicionMult = 3
				rt.GossipWANGossipInterval = 100 * time.Millisecond
				rt.GossipWANProbeInterval = 100 * time.Millisecond
				rt.GossipWANProbeTimeout = 100 * time.Millisecond
				rt.GossipWANSuspicionMult = 3
				rt.ConsulServerHealthInterval = 10 * time.Millisecond
				rt.GRPCPort = 8502
				rt.GRPCAddrs = []net.Addr{tcpAddr("127.0.0.1:8502")}
			},
		},
		{
			desc: "-disable-host-node-id",
			args: []string{
				`-disable-host-node-id`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.DisableHostNodeID = true
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-disable-keyring-file",
			args: []string{
				`-disable-keyring-file`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.DisableKeyringFile = true
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-dns-port",
			args: []string{
				`-dns-port=123`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.DNSPort = 123
				rt.DNSAddrs = []net.Addr{tcpAddr("127.0.0.1:123"), udpAddr("127.0.0.1:123")}
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-domain",
			args: []string{
				`-domain=a`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.DNSDomain = "a"
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-alt-domain",
			args: []string{
				`-alt-domain=alt`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.DNSAltDomain = "alt"
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-alt-domain can't be prefixed by DC",
			args: []string{
				`-datacenter=a`,
				`-alt-domain=a.alt`,
				`-data-dir=` + dataDir,
			},
			err: "alt_domain cannot start with {service,connect,node,query,addr,a}",
		},
		{
			desc: "-alt-domain can't be prefixed by service",
			args: []string{
				`-alt-domain=service.alt`,
				`-data-dir=` + dataDir,
			},
			err: "alt_domain cannot start with {service,connect,node,query,addr,dc1}",
		},
		{
			desc: "-alt-domain can be prefixed by non-keywords",
			args: []string{
				`-alt-domain=mydomain.alt`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.DNSAltDomain = "mydomain.alt"
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-alt-domain can't be prefixed by DC",
			args: []string{
				`-alt-domain=dc1.alt`,
				`-data-dir=` + dataDir,
			},
			err: "alt_domain cannot start with {service,connect,node,query,addr,dc1}",
		},
		{
			desc: "-enable-script-checks",
			args: []string{
				`-enable-script-checks`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.EnableLocalScriptChecks = true
				rt.EnableRemoteScriptChecks = true
				rt.DataDir = dataDir
			},
			warns: []string{remoteScriptCheckSecurityWarning},
		},
		{
			desc: "-encrypt",
			args: []string{
				`-encrypt=pUqJrVyVRj5jsiYEkM/tFQYfWyJIv4s3XkvDwy7Cu5s=`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.EncryptKey = "pUqJrVyVRj5jsiYEkM/tFQYfWyJIv4s3XkvDwy7Cu5s="
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-config-format disabled, skip unknown files",
			args: []string{
				`-data-dir=` + dataDir,
				`-config-dir`, filepath.Join(dataDir, "conf"),
			},
			patch: func(rt *RuntimeConfig) {
				rt.Datacenter = "a"
				rt.ACLDatacenter = "a"
				rt.PrimaryDatacenter = "a"
				rt.DataDir = dataDir
			},
			pre: func() {
				writeFile(filepath.Join(dataDir, "conf", "valid.json"), []byte(`{"datacenter":"a"}`))
				writeFile(filepath.Join(dataDir, "conf", "invalid.skip"), []byte(`NOPE`))
			},
			warns: []string{
				"skipping file " + filepath.Join(dataDir, "conf", "invalid.skip") + ", extension must be .hcl or .json, or config format must be set",
			},
		},
		{
			desc: "-config-format=json",
			args: []string{
				`-data-dir=` + dataDir,
				`-config-format=json`,
				`-config-file`, filepath.Join(dataDir, "conf"),
			},
			patch: func(rt *RuntimeConfig) {
				rt.Datacenter = "a"
				rt.ACLDatacenter = "a"
				rt.PrimaryDatacenter = "a"
				rt.DataDir = dataDir
			},
			pre: func() {
				writeFile(filepath.Join(dataDir, "conf"), []byte(`{"datacenter":"a"}`))
			},
		},
		{
			desc: "-config-format=hcl",
			args: []string{
				`-data-dir=` + dataDir,
				`-config-format=hcl`,
				`-config-file`, filepath.Join(dataDir, "conf"),
			},
			patch: func(rt *RuntimeConfig) {
				rt.Datacenter = "a"
				rt.ACLDatacenter = "a"
				rt.PrimaryDatacenter = "a"
				rt.DataDir = dataDir
			},
			pre: func() {
				writeFile(filepath.Join(dataDir, "conf"), []byte(`datacenter = "a"`))
			},
		},
		{
			desc: "-http-port",
			args: []string{
				`-http-port=123`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.HTTPPort = 123
				rt.HTTPAddrs = []net.Addr{tcpAddr("127.0.0.1:123")}
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-join",
			args: []string{
				`-join=a`,
				`-join=b`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.StartJoinAddrsLAN = []string{"a", "b"}
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-join-wan",
			args: []string{
				`-join-wan=a`,
				`-join-wan=b`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.StartJoinAddrsWAN = []string{"a", "b"}
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-log-level",
			args: []string{
				`-log-level=a`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.Logging.LogLevel = "a"
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-log-json",
			args: []string{
				`-log-json`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.Logging.LogJSON = true
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-log-rotate-max-files",
			args: []string{
				`-log-rotate-max-files=2`,
				`-data-dir=` + dataDir,
			},
			json: []string{`{ "log_rotate_max_files": 2 }`},
			hcl:  []string{`log_rotate_max_files = 2`},
			patch: func(rt *RuntimeConfig) {
				rt.Logging.LogRotateMaxFiles = 2
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-node",
			args: []string{
				`-node=a`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.NodeName = "a"
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-node-id",
			args: []string{
				`-node-id=a`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.NodeID = "a"
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-node-meta",
			args: []string{
				`-node-meta=a:b`,
				`-node-meta=c:d`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.NodeMeta = map[string]string{"a": "b", "c": "d"}
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-non-voting-server",
			args: []string{
				`-non-voting-server`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.ReadReplica = true
				rt.DataDir = dataDir
			},
			warns: enterpriseReadReplicaWarnings,
		},
		{
			desc: "-pid-file",
			args: []string{
				`-pid-file=a`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.PidFile = "a"
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-primary-gateway",
			args: []string{
				`-server`,
				`-datacenter=dc2`,
				`-primary-gateway=a`,
				`-primary-gateway=b`,
				`-data-dir=` + dataDir,
			},
			json: []string{`{ "primary_datacenter": "dc1" }`},
			hcl:  []string{`primary_datacenter = "dc1"`},
			patch: func(rt *RuntimeConfig) {
				rt.Datacenter = "dc2"
				rt.PrimaryDatacenter = "dc1"
				rt.ACLDatacenter = "dc1"
				rt.PrimaryGateways = []string{"a", "b"}
				rt.DataDir = dataDir
				// server things
				rt.ServerMode = true
				rt.LeaveOnTerm = false
				rt.SkipLeaveOnInt = true
			},
		},
		{
			desc: "-protocol",
			args: []string{
				`-protocol=1`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.RPCProtocol = 1
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-raft-protocol",
			args: []string{
				`-raft-protocol=3`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.RaftProtocol = 3
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-raft-protocol unsupported",
			args: []string{
				`-raft-protocol=2`,
				`-data-dir=` + dataDir,
			},
			err: "raft_protocol version 2 is not supported by this version of Consul",
		},
		{
			desc: "-recursor",
			args: []string{
				`-recursor=1.2.3.4`,
				`-recursor=5.6.7.8`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.DNSRecursors = []string{"1.2.3.4", "5.6.7.8"}
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-rejoin",
			args: []string{
				`-rejoin`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.RejoinAfterLeave = true
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-retry-interval",
			args: []string{
				`-retry-interval=5s`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.RetryJoinIntervalLAN = 5 * time.Second
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-retry-interval-wan",
			args: []string{
				`-retry-interval-wan=5s`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.RetryJoinIntervalWAN = 5 * time.Second
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-retry-join",
			args: []string{
				`-retry-join=a`,
				`-retry-join=b`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.RetryJoinLAN = []string{"a", "b"}
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-retry-join-wan",
			args: []string{
				`-retry-join-wan=a`,
				`-retry-join-wan=b`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.RetryJoinWAN = []string{"a", "b"}
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-retry-max",
			args: []string{
				`-retry-max=1`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.RetryJoinMaxAttemptsLAN = 1
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-retry-max-wan",
			args: []string{
				`-retry-max-wan=1`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.RetryJoinMaxAttemptsWAN = 1
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-serf-lan-bind",
			args: []string{
				`-serf-lan-bind=1.2.3.4`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.SerfBindAddrLAN = tcpAddr("1.2.3.4:8301")
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-serf-lan-port",
			args: []string{
				`-serf-lan-port=123`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.SerfPortLAN = 123
				rt.SerfAdvertiseAddrLAN = tcpAddr("10.0.0.1:123")
				rt.SerfBindAddrLAN = tcpAddr("0.0.0.0:123")
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-serf-wan-bind",
			args: []string{
				`-serf-wan-bind=1.2.3.4`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.SerfBindAddrWAN = tcpAddr("1.2.3.4:8302")
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-serf-wan-port",
			args: []string{
				`-serf-wan-port=123`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.SerfPortWAN = 123
				rt.SerfAdvertiseAddrWAN = tcpAddr("10.0.0.1:123")
				rt.SerfBindAddrWAN = tcpAddr("0.0.0.0:123")
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-server",
			args: []string{
				`-server`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.ServerMode = true
				rt.LeaveOnTerm = false
				rt.SkipLeaveOnInt = true
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-server-port",
			args: []string{
				`-server-port=123`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.ServerPort = 123
				rt.RPCAdvertiseAddr = tcpAddr("10.0.0.1:123")
				rt.RPCBindAddr = tcpAddr("0.0.0.0:123")
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-syslog",
			args: []string{
				`-syslog`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.Logging.EnableSyslog = true
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-ui",
			args: []string{
				`-ui`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.UIConfig.Enabled = true
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-ui-dir",
			args: []string{
				`-ui-dir=a`,
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.UIConfig.Dir = "a"
				rt.DataDir = dataDir
			},
		},
		{
			desc: "-ui-content-path",
			args: []string{
				`-ui-content-path=/a/b`,
				`-data-dir=` + dataDir,
			},

			patch: func(rt *RuntimeConfig) {
				rt.UIConfig.ContentPath = "/a/b/"
				rt.DataDir = dataDir
			},
		},

		// ------------------------------------------------------------
		// ports and addresses
		//

		{
			desc: "bind addr any v4",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{ "bind_addr":"0.0.0.0" }`},
			hcl:  []string{`bind_addr = "0.0.0.0"`},
			patch: func(rt *RuntimeConfig) {
				rt.AdvertiseAddrLAN = ipAddr("10.0.0.1")
				rt.AdvertiseAddrWAN = ipAddr("10.0.0.1")
				rt.BindAddr = ipAddr("0.0.0.0")
				rt.RPCAdvertiseAddr = tcpAddr("10.0.0.1:8300")
				rt.RPCBindAddr = tcpAddr("0.0.0.0:8300")
				rt.SerfAdvertiseAddrLAN = tcpAddr("10.0.0.1:8301")
				rt.SerfAdvertiseAddrWAN = tcpAddr("10.0.0.1:8302")
				rt.SerfBindAddrLAN = tcpAddr("0.0.0.0:8301")
				rt.SerfBindAddrWAN = tcpAddr("0.0.0.0:8302")
				rt.TaggedAddresses = map[string]string{
					"lan":      "10.0.0.1",
					"lan_ipv4": "10.0.0.1",
					"wan":      "10.0.0.1",
					"wan_ipv4": "10.0.0.1",
				}
				rt.DataDir = dataDir
			},
		},
		{
			desc: "bind addr any v6",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{ "bind_addr":"::" }`},
			hcl:  []string{`bind_addr = "::"`},
			patch: func(rt *RuntimeConfig) {
				rt.AdvertiseAddrLAN = ipAddr("dead:beef::1")
				rt.AdvertiseAddrWAN = ipAddr("dead:beef::1")
				rt.BindAddr = ipAddr("::")
				rt.RPCAdvertiseAddr = tcpAddr("[dead:beef::1]:8300")
				rt.RPCBindAddr = tcpAddr("[::]:8300")
				rt.SerfAdvertiseAddrLAN = tcpAddr("[dead:beef::1]:8301")
				rt.SerfAdvertiseAddrWAN = tcpAddr("[dead:beef::1]:8302")
				rt.SerfBindAddrLAN = tcpAddr("[::]:8301")
				rt.SerfBindAddrWAN = tcpAddr("[::]:8302")
				rt.TaggedAddresses = map[string]string{
					"lan":      "dead:beef::1",
					"lan_ipv6": "dead:beef::1",
					"wan":      "dead:beef::1",
					"wan_ipv6": "dead:beef::1",
				}
				rt.DataDir = dataDir
			},
			publicv6: func() ([]*net.IPAddr, error) {
				return []*net.IPAddr{ipAddr("dead:beef::1")}, nil
			},
		},
		{
			desc: "bind addr any and advertise set should not detect",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{ "bind_addr":"0.0.0.0", "advertise_addr": "1.2.3.4" }`},
			hcl:  []string{`bind_addr = "0.0.0.0" advertise_addr = "1.2.3.4"`},
			patch: func(rt *RuntimeConfig) {
				rt.AdvertiseAddrLAN = ipAddr("1.2.3.4")
				rt.AdvertiseAddrWAN = ipAddr("1.2.3.4")
				rt.BindAddr = ipAddr("0.0.0.0")
				rt.RPCAdvertiseAddr = tcpAddr("1.2.3.4:8300")
				rt.RPCBindAddr = tcpAddr("0.0.0.0:8300")
				rt.SerfAdvertiseAddrLAN = tcpAddr("1.2.3.4:8301")
				rt.SerfAdvertiseAddrWAN = tcpAddr("1.2.3.4:8302")
				rt.SerfBindAddrLAN = tcpAddr("0.0.0.0:8301")
				rt.SerfBindAddrWAN = tcpAddr("0.0.0.0:8302")
				rt.TaggedAddresses = map[string]string{
					"lan":      "1.2.3.4",
					"lan_ipv4": "1.2.3.4",
					"wan":      "1.2.3.4",
					"wan_ipv4": "1.2.3.4",
				}
				rt.DataDir = dataDir
			},
			privatev4: func() ([]*net.IPAddr, error) {
				return nil, fmt.Errorf("should not detect advertise_addr")
			},
		},
		{
			desc: "client addr and ports == 0",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
					"client_addr":"0.0.0.0",
					"ports":{}
				}`},
			hcl: []string{`
					client_addr = "0.0.0.0"
					ports {}
				`},
			patch: func(rt *RuntimeConfig) {
				rt.ClientAddrs = []*net.IPAddr{ipAddr("0.0.0.0")}
				rt.DNSAddrs = []net.Addr{tcpAddr("0.0.0.0:8600"), udpAddr("0.0.0.0:8600")}
				rt.HTTPAddrs = []net.Addr{tcpAddr("0.0.0.0:8500")}
				rt.DataDir = dataDir
			},
		},
		{
			desc: "client addr and ports < 0",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
					"client_addr":"0.0.0.0",
					"ports": { "dns":-1, "http":-2, "https":-3, "grpc":-4 }
				}`},
			hcl: []string{`
					client_addr = "0.0.0.0"
					ports { dns = -1 http = -2 https = -3 grpc = -4 }
				`},
			patch: func(rt *RuntimeConfig) {
				rt.ClientAddrs = []*net.IPAddr{ipAddr("0.0.0.0")}
				rt.DNSPort = -1
				rt.DNSAddrs = nil
				rt.HTTPPort = -1
				rt.HTTPAddrs = nil
				// HTTPS and gRPC default to disabled so shouldn't be different from
				// default rt.
				rt.DataDir = dataDir
			},
		},
		{
			desc: "client addr and ports > 0",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
					"client_addr":"0.0.0.0",
					"ports":{ "dns": 1, "http": 2, "https": 3, "grpc": 4 }
				}`},
			hcl: []string{`
					client_addr = "0.0.0.0"
					ports { dns = 1 http = 2 https = 3 grpc = 4 }
				`},
			patch: func(rt *RuntimeConfig) {
				rt.ClientAddrs = []*net.IPAddr{ipAddr("0.0.0.0")}
				rt.DNSPort = 1
				rt.DNSAddrs = []net.Addr{tcpAddr("0.0.0.0:1"), udpAddr("0.0.0.0:1")}
				rt.HTTPPort = 2
				rt.HTTPAddrs = []net.Addr{tcpAddr("0.0.0.0:2")}
				rt.HTTPSPort = 3
				rt.HTTPSAddrs = []net.Addr{tcpAddr("0.0.0.0:3")}
				rt.GRPCPort = 4
				rt.GRPCAddrs = []net.Addr{tcpAddr("0.0.0.0:4")}
				rt.DataDir = dataDir
			},
		},

		{
			desc: "client addr, addresses and ports == 0",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
					"client_addr":"0.0.0.0",
					"addresses": { "dns": "1.1.1.1", "http": "2.2.2.2", "https": "3.3.3.3", "grpc": "4.4.4.4" },
					"ports":{}
				}`},
			hcl: []string{`
					client_addr = "0.0.0.0"
					addresses = { dns = "1.1.1.1" http = "2.2.2.2" https = "3.3.3.3" grpc = "4.4.4.4" }
					ports {}
				`},
			patch: func(rt *RuntimeConfig) {
				rt.ClientAddrs = []*net.IPAddr{ipAddr("0.0.0.0")}
				rt.DNSAddrs = []net.Addr{tcpAddr("1.1.1.1:8600"), udpAddr("1.1.1.1:8600")}
				rt.HTTPAddrs = []net.Addr{tcpAddr("2.2.2.2:8500")}
				// HTTPS and gRPC default to disabled so shouldn't be different from
				// default rt.
				rt.DataDir = dataDir
			},
		},
		{
			desc: "client addr, addresses and ports < 0",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
					"client_addr":"0.0.0.0",
					"addresses": { "dns": "1.1.1.1", "http": "2.2.2.2", "https": "3.3.3.3", "grpc": "4.4.4.4" },
					"ports": { "dns":-1, "http":-2, "https":-3, "grpc":-4 }
				}`},
			hcl: []string{`
					client_addr = "0.0.0.0"
					addresses = { dns = "1.1.1.1" http = "2.2.2.2" https = "3.3.3.3" grpc = "4.4.4.4" }
					ports { dns = -1 http = -2 https = -3 grpc = -4 }
				`},
			patch: func(rt *RuntimeConfig) {
				rt.ClientAddrs = []*net.IPAddr{ipAddr("0.0.0.0")}
				rt.DNSPort = -1
				rt.DNSAddrs = nil
				rt.HTTPPort = -1
				rt.HTTPAddrs = nil
				// HTTPS and gRPC default to disabled so shouldn't be different from
				// default rt.
				rt.DataDir = dataDir
			},
		},
		{
			desc: "client addr, addresses and ports",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
					"client_addr": "0.0.0.0",
					"addresses": { "dns": "1.1.1.1", "http": "2.2.2.2", "https": "3.3.3.3", "grpc": "4.4.4.4" },
					"ports":{ "dns":1, "http":2, "https":3, "grpc":4 }
				}`},
			hcl: []string{`
					client_addr = "0.0.0.0"
					addresses = { dns = "1.1.1.1" http = "2.2.2.2" https = "3.3.3.3" grpc = "4.4.4.4" }
					ports { dns = 1 http = 2 https = 3 grpc = 4 }
				`},
			patch: func(rt *RuntimeConfig) {
				rt.ClientAddrs = []*net.IPAddr{ipAddr("0.0.0.0")}
				rt.DNSPort = 1
				rt.DNSAddrs = []net.Addr{tcpAddr("1.1.1.1:1"), udpAddr("1.1.1.1:1")}
				rt.HTTPPort = 2
				rt.HTTPAddrs = []net.Addr{tcpAddr("2.2.2.2:2")}
				rt.HTTPSPort = 3
				rt.HTTPSAddrs = []net.Addr{tcpAddr("3.3.3.3:3")}
				rt.GRPCPort = 4
				rt.GRPCAddrs = []net.Addr{tcpAddr("4.4.4.4:4")}
				rt.DataDir = dataDir
			},
		},
		{
			desc: "client template and ports",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
					"client_addr": "{{ printf \"1.2.3.4 2001:db8::1\" }}",
					"ports":{ "dns":1, "http":2, "https":3, "grpc":4 }
				}`},
			hcl: []string{`
					client_addr = "{{ printf \"1.2.3.4 2001:db8::1\" }}"
					ports { dns = 1 http = 2 https = 3 grpc = 4 }
				`},
			patch: func(rt *RuntimeConfig) {
				rt.ClientAddrs = []*net.IPAddr{ipAddr("1.2.3.4"), ipAddr("2001:db8::1")}
				rt.DNSPort = 1
				rt.DNSAddrs = []net.Addr{tcpAddr("1.2.3.4:1"), tcpAddr("[2001:db8::1]:1"), udpAddr("1.2.3.4:1"), udpAddr("[2001:db8::1]:1")}
				rt.HTTPPort = 2
				rt.HTTPAddrs = []net.Addr{tcpAddr("1.2.3.4:2"), tcpAddr("[2001:db8::1]:2")}
				rt.HTTPSPort = 3
				rt.HTTPSAddrs = []net.Addr{tcpAddr("1.2.3.4:3"), tcpAddr("[2001:db8::1]:3")}
				rt.GRPCPort = 4
				rt.GRPCAddrs = []net.Addr{tcpAddr("1.2.3.4:4"), tcpAddr("[2001:db8::1]:4")}
				rt.DataDir = dataDir
			},
		},
		{
			desc: "client, address template and ports",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
					"client_addr": "{{ printf \"1.2.3.4 2001:db8::1\" }}",
					"addresses": {
						"dns": "{{ printf \"1.1.1.1 2001:db8::10 \" }}",
						"http": "{{ printf \"2.2.2.2 unix://http 2001:db8::20 \" }}",
						"https": "{{ printf \"3.3.3.3 unix://https 2001:db8::30 \" }}",
						"grpc": "{{ printf \"4.4.4.4 unix://grpc 2001:db8::40 \" }}"
					},
					"ports":{ "dns":1, "http":2, "https":3, "grpc":4 }
				}`},
			hcl: []string{`
					client_addr = "{{ printf \"1.2.3.4 2001:db8::1\" }}"
					addresses = {
						dns = "{{ printf \"1.1.1.1 2001:db8::10 \" }}"
						http = "{{ printf \"2.2.2.2 unix://http 2001:db8::20 \" }}"
						https = "{{ printf \"3.3.3.3 unix://https 2001:db8::30 \" }}"
						grpc = "{{ printf \"4.4.4.4 unix://grpc 2001:db8::40 \" }}"
					}
					ports { dns = 1 http = 2 https = 3 grpc = 4 }
				`},
			patch: func(rt *RuntimeConfig) {
				rt.ClientAddrs = []*net.IPAddr{ipAddr("1.2.3.4"), ipAddr("2001:db8::1")}
				rt.DNSPort = 1
				rt.DNSAddrs = []net.Addr{tcpAddr("1.1.1.1:1"), tcpAddr("[2001:db8::10]:1"), udpAddr("1.1.1.1:1"), udpAddr("[2001:db8::10]:1")}
				rt.HTTPPort = 2
				rt.HTTPAddrs = []net.Addr{tcpAddr("2.2.2.2:2"), unixAddr("unix://http"), tcpAddr("[2001:db8::20]:2")}
				rt.HTTPSPort = 3
				rt.HTTPSAddrs = []net.Addr{tcpAddr("3.3.3.3:3"), unixAddr("unix://https"), tcpAddr("[2001:db8::30]:3")}
				rt.GRPCPort = 4
				rt.GRPCAddrs = []net.Addr{tcpAddr("4.4.4.4:4"), unixAddr("unix://grpc"), tcpAddr("[2001:db8::40]:4")}
				rt.DataDir = dataDir
			},
		},
		{
			desc: "advertise address lan template",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{ "advertise_addr": "{{ printf \"1.2.3.4\" }}" }`},
			hcl:  []string{`advertise_addr = "{{ printf \"1.2.3.4\" }}"`},
			patch: func(rt *RuntimeConfig) {
				rt.AdvertiseAddrLAN = ipAddr("1.2.3.4")
				rt.AdvertiseAddrWAN = ipAddr("1.2.3.4")
				rt.RPCAdvertiseAddr = tcpAddr("1.2.3.4:8300")
				rt.SerfAdvertiseAddrLAN = tcpAddr("1.2.3.4:8301")
				rt.SerfAdvertiseAddrWAN = tcpAddr("1.2.3.4:8302")
				rt.TaggedAddresses = map[string]string{
					"lan":      "1.2.3.4",
					"lan_ipv4": "1.2.3.4",
					"wan":      "1.2.3.4",
					"wan_ipv4": "1.2.3.4",
				}
				rt.DataDir = dataDir
			},
		},
		{
			desc: "advertise address wan template",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{ "advertise_addr_wan": "{{ printf \"1.2.3.4\" }}" }`},
			hcl:  []string{`advertise_addr_wan = "{{ printf \"1.2.3.4\" }}"`},
			patch: func(rt *RuntimeConfig) {
				rt.AdvertiseAddrWAN = ipAddr("1.2.3.4")
				rt.SerfAdvertiseAddrWAN = tcpAddr("1.2.3.4:8302")
				rt.TaggedAddresses = map[string]string{
					"lan":      "10.0.0.1",
					"lan_ipv4": "10.0.0.1",
					"wan":      "1.2.3.4",
					"wan_ipv4": "1.2.3.4",
				}
				rt.DataDir = dataDir
			},
		},
		{
			desc: "advertise address lan with ports",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
				"ports": {
					"server": 1000,
					"serf_lan": 2000,
					"serf_wan": 3000
				},
				"advertise_addr": "1.2.3.4"
			}`},
			hcl: []string{`
				ports {
					server = 1000
					serf_lan = 2000
					serf_wan = 3000
				}
				advertise_addr = "1.2.3.4"
			`},
			patch: func(rt *RuntimeConfig) {
				rt.AdvertiseAddrLAN = ipAddr("1.2.3.4")
				rt.AdvertiseAddrWAN = ipAddr("1.2.3.4")
				rt.RPCAdvertiseAddr = tcpAddr("1.2.3.4:1000")
				rt.RPCBindAddr = tcpAddr("0.0.0.0:1000")
				rt.SerfAdvertiseAddrLAN = tcpAddr("1.2.3.4:2000")
				rt.SerfAdvertiseAddrWAN = tcpAddr("1.2.3.4:3000")
				rt.SerfBindAddrLAN = tcpAddr("0.0.0.0:2000")
				rt.SerfBindAddrWAN = tcpAddr("0.0.0.0:3000")
				rt.SerfPortLAN = 2000
				rt.SerfPortWAN = 3000
				rt.ServerPort = 1000
				rt.TaggedAddresses = map[string]string{
					"lan":      "1.2.3.4",
					"lan_ipv4": "1.2.3.4",
					"wan":      "1.2.3.4",
					"wan_ipv4": "1.2.3.4",
				}
				rt.DataDir = dataDir
			},
		},
		{
			desc: "advertise address wan with ports",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
				"ports": {
					"server": 1000,
					"serf_lan": 2000,
					"serf_wan": 3000
				},
				"advertise_addr_wan": "1.2.3.4"
			}`},
			hcl: []string{`
				ports {
					server = 1000
					serf_lan = 2000
					serf_wan = 3000
				}
				advertise_addr_wan = "1.2.3.4"
			`},
			patch: func(rt *RuntimeConfig) {
				rt.AdvertiseAddrLAN = ipAddr("10.0.0.1")
				rt.AdvertiseAddrWAN = ipAddr("1.2.3.4")
				rt.RPCAdvertiseAddr = tcpAddr("10.0.0.1:1000")
				rt.RPCBindAddr = tcpAddr("0.0.0.0:1000")
				rt.SerfAdvertiseAddrLAN = tcpAddr("10.0.0.1:2000")
				rt.SerfAdvertiseAddrWAN = tcpAddr("1.2.3.4:3000")
				rt.SerfBindAddrLAN = tcpAddr("0.0.0.0:2000")
				rt.SerfBindAddrWAN = tcpAddr("0.0.0.0:3000")
				rt.SerfPortLAN = 2000
				rt.SerfPortWAN = 3000
				rt.ServerPort = 1000
				rt.TaggedAddresses = map[string]string{
					"lan":      "10.0.0.1",
					"lan_ipv4": "10.0.0.1",
					"wan":      "1.2.3.4",
					"wan_ipv4": "1.2.3.4",
				}
				rt.DataDir = dataDir
			},
		},
		{
			desc: "allow disabling serf wan port",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
				"ports": {
					"serf_wan": -1
				},
				"advertise_addr_wan": "1.2.3.4"
			}`},
			hcl: []string{`
				ports {
					serf_wan = -1
				}
				advertise_addr_wan = "1.2.3.4"
			`},
			patch: func(rt *RuntimeConfig) {
				rt.AdvertiseAddrWAN = ipAddr("1.2.3.4")
				rt.SerfAdvertiseAddrWAN = nil
				rt.SerfBindAddrWAN = nil
				rt.TaggedAddresses = map[string]string{
					"lan":      "10.0.0.1",
					"lan_ipv4": "10.0.0.1",
					"wan":      "1.2.3.4",
					"wan_ipv4": "1.2.3.4",
				}
				rt.DataDir = dataDir
				rt.SerfPortWAN = -1
			},
		},
		{
			desc: "serf bind address lan template",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{ "serf_lan": "{{ printf \"1.2.3.4\" }}" }`},
			hcl:  []string{`serf_lan = "{{ printf \"1.2.3.4\" }}"`},
			patch: func(rt *RuntimeConfig) {
				rt.SerfBindAddrLAN = tcpAddr("1.2.3.4:8301")
				rt.DataDir = dataDir
			},
		},
		{
			desc: "serf bind address wan template",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{ "serf_wan": "{{ printf \"1.2.3.4\" }}" }`},
			hcl:  []string{`serf_wan = "{{ printf \"1.2.3.4\" }}"`},
			patch: func(rt *RuntimeConfig) {
				rt.SerfBindAddrWAN = tcpAddr("1.2.3.4:8302")
				rt.DataDir = dataDir
			},
		},
		{
			desc: "dns recursor templates with deduplication",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{ "recursors": [ "{{ printf \"5.6.7.8:9999\" }}", "{{ printf \"1.2.3.4\" }}", "{{ printf \"5.6.7.8:9999\" }}" ] }`},
			hcl:  []string{`recursors = [ "{{ printf \"5.6.7.8:9999\" }}", "{{ printf \"1.2.3.4\" }}", "{{ printf \"5.6.7.8:9999\" }}" ] `},
			patch: func(rt *RuntimeConfig) {
				rt.DNSRecursors = []string{"5.6.7.8:9999", "1.2.3.4"}
				rt.DataDir = dataDir
			},
		},
		{
			desc: "start_join address template",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{ "start_join": ["{{ printf \"1.2.3.4 4.3.2.1\" }}"] }`},
			hcl:  []string{`start_join = ["{{ printf \"1.2.3.4 4.3.2.1\" }}"]`},
			patch: func(rt *RuntimeConfig) {
				rt.StartJoinAddrsLAN = []string{"1.2.3.4", "4.3.2.1"}
				rt.DataDir = dataDir
			},
		},
		{
			desc: "start_join_wan address template",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{ "start_join_wan": ["{{ printf \"1.2.3.4 4.3.2.1\" }}"] }`},
			hcl:  []string{`start_join_wan = ["{{ printf \"1.2.3.4 4.3.2.1\" }}"]`},
			patch: func(rt *RuntimeConfig) {
				rt.StartJoinAddrsWAN = []string{"1.2.3.4", "4.3.2.1"}
				rt.DataDir = dataDir
			},
		},
		{
			desc: "retry_join address template",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{ "retry_join": ["{{ printf \"1.2.3.4 4.3.2.1\" }}"] }`},
			hcl:  []string{`retry_join = ["{{ printf \"1.2.3.4 4.3.2.1\" }}"]`},
			patch: func(rt *RuntimeConfig) {
				rt.RetryJoinLAN = []string{"1.2.3.4", "4.3.2.1"}
				rt.DataDir = dataDir
			},
		},
		{
			desc: "retry_join_wan address template",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{ "retry_join_wan": ["{{ printf \"1.2.3.4 4.3.2.1\" }}"] }`},
			hcl:  []string{`retry_join_wan = ["{{ printf \"1.2.3.4 4.3.2.1\" }}"]`},
			patch: func(rt *RuntimeConfig) {
				rt.RetryJoinWAN = []string{"1.2.3.4", "4.3.2.1"}
				rt.DataDir = dataDir
			},
		},
		{
			desc: "min/max ports for dynamic exposed listeners",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
				"ports": {
					"expose_min_port": 1234,
					"expose_max_port": 5678
				}
			}`},
			hcl: []string{`
				ports {
					expose_min_port = 1234
					expose_max_port = 5678
				}
			`},
			patch: func(rt *RuntimeConfig) {
				rt.ExposeMinPort = 1234
				rt.ExposeMaxPort = 5678
				rt.DataDir = dataDir
			},
		},
		{
			desc: "defaults for dynamic exposed listeners",
			args: []string{`-data-dir=` + dataDir},
			patch: func(rt *RuntimeConfig) {
				rt.ExposeMinPort = 21500
				rt.ExposeMaxPort = 21755
				rt.DataDir = dataDir
			},
		},

		// ------------------------------------------------------------
		// precedence rules
		//

		{
			desc: "precedence: merge order",
			args: []string{`-data-dir=` + dataDir},
			json: []string{
				`{
						"bootstrap": true,
						"bootstrap_expect": 1,
						"datacenter": "a",
						"start_join": ["a", "b"],
						"node_meta": {"a":"b"}
					}`,
				`{
						"bootstrap": false,
						"bootstrap_expect": 0,
						"datacenter":"b",
						"start_join": ["c", "d"],
						"node_meta": {"a":"c"}
					}`,
			},
			hcl: []string{
				`
					bootstrap = true
					bootstrap_expect = 1
					datacenter = "a"
					start_join = ["a", "b"]
					node_meta = { "a" = "b" }
					`,
				`
					bootstrap = false
					bootstrap_expect = 0
					datacenter = "b"
					start_join = ["c", "d"]
					node_meta = { "a" = "c" }
					`,
			},
			patch: func(rt *RuntimeConfig) {
				rt.Bootstrap = false
				rt.BootstrapExpect = 0
				rt.Datacenter = "b"
				rt.ACLDatacenter = "b"
				rt.PrimaryDatacenter = "b"
				rt.StartJoinAddrsLAN = []string{"a", "b", "c", "d"}
				rt.NodeMeta = map[string]string{"a": "c"}
				rt.DataDir = dataDir
			},
		},
		{
			desc: "precedence: flag before file",
			json: []string{
				`{
						"advertise_addr": "1.2.3.4",
						"advertise_addr_wan": "5.6.7.8",
						"bootstrap":true,
						"bootstrap_expect": 3,
						"datacenter":"a",
						"node_meta": {"a":"b"},
						"recursors":["1.2.3.5", "5.6.7.9"],
						"serf_lan": "a",
						"serf_wan": "a",
						"start_join":["a", "b"]
					}`,
			},
			hcl: []string{
				`
					advertise_addr = "1.2.3.4"
					advertise_addr_wan = "5.6.7.8"
					bootstrap = true
					bootstrap_expect = 3
					datacenter = "a"
					node_meta = { "a" = "b" }
					recursors = ["1.2.3.5", "5.6.7.9"]
					serf_lan = "a"
					serf_wan = "a"
					start_join = ["a", "b"]
					`,
			},
			args: []string{
				`-advertise=1.1.1.1`,
				`-advertise-wan=2.2.2.2`,
				`-bootstrap=false`,
				`-bootstrap-expect=0`,
				`-datacenter=b`,
				`-data-dir=` + dataDir,
				`-join`, `c`, `-join=d`,
				`-node-meta=a:c`,
				`-recursor`, `1.2.3.6`, `-recursor=5.6.7.10`,
				`-serf-lan-bind=3.3.3.3`,
				`-serf-wan-bind=4.4.4.4`,
			},
			patch: func(rt *RuntimeConfig) {
				rt.AdvertiseAddrLAN = ipAddr("1.1.1.1")
				rt.AdvertiseAddrWAN = ipAddr("2.2.2.2")
				rt.RPCAdvertiseAddr = tcpAddr("1.1.1.1:8300")
				rt.SerfAdvertiseAddrLAN = tcpAddr("1.1.1.1:8301")
				rt.SerfAdvertiseAddrWAN = tcpAddr("2.2.2.2:8302")
				rt.Datacenter = "b"
				rt.ACLDatacenter = "b"
				rt.PrimaryDatacenter = "b"
				rt.DNSRecursors = []string{"1.2.3.6", "5.6.7.10", "1.2.3.5", "5.6.7.9"}
				rt.NodeMeta = map[string]string{"a": "c"}
				rt.SerfBindAddrLAN = tcpAddr("3.3.3.3:8301")
				rt.SerfBindAddrWAN = tcpAddr("4.4.4.4:8302")
				rt.StartJoinAddrsLAN = []string{"c", "d", "a", "b"}
				rt.TaggedAddresses = map[string]string{
					"lan":      "1.1.1.1",
					"lan_ipv4": "1.1.1.1",
					"wan":      "2.2.2.2",
					"wan_ipv4": "2.2.2.2",
				}
				rt.DataDir = dataDir
			},
		},

		// ------------------------------------------------------------
		// transformations
		//

		{
			desc: "raft performance scaling",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{ "performance": { "raft_multiplier": 9} }`},
			hcl:  []string{`performance = { raft_multiplier=9 }`},
			patch: func(rt *RuntimeConfig) {
				rt.ConsulRaftElectionTimeout = 9 * 1000 * time.Millisecond
				rt.ConsulRaftHeartbeatTimeout = 9 * 1000 * time.Millisecond
				rt.ConsulRaftLeaderLeaseTimeout = 9 * 500 * time.Millisecond
				rt.DataDir = dataDir
			},
		},

		{
			desc: "Serf Allowed CIDRS LAN, multiple values from flags",
			args: []string{`-data-dir=` + dataDir, `-serf-lan-allowed-cidrs=127.0.0.0/4`, `-serf-lan-allowed-cidrs=192.168.0.0/24`},
			json: []string{},
			hcl:  []string{},
			patch: func(rt *RuntimeConfig) {
				rt.DataDir = dataDir
				rt.SerfAllowedCIDRsLAN = []net.IPNet{*(parseCIDR(t, "127.0.0.0/4")), *(parseCIDR(t, "192.168.0.0/24"))}
			},
		},
		{
			desc: "Serf Allowed CIDRS LAN/WAN, multiple values from HCL/JSON",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{"serf_lan_allowed_cidrs": ["127.0.0.0/4", "192.168.0.0/24"]}`,
				`{"serf_wan_allowed_cidrs": ["10.228.85.46/25"]}`},
			hcl: []string{`serf_lan_allowed_cidrs=["127.0.0.0/4", "192.168.0.0/24"]`,
				`serf_wan_allowed_cidrs=["10.228.85.46/25"]`},
			patch: func(rt *RuntimeConfig) {
				rt.DataDir = dataDir
				rt.SerfAllowedCIDRsLAN = []net.IPNet{*(parseCIDR(t, "127.0.0.0/4")), *(parseCIDR(t, "192.168.0.0/24"))}
				rt.SerfAllowedCIDRsWAN = []net.IPNet{*(parseCIDR(t, "10.228.85.46/25"))}
			},
		},
		{
			desc: "Serf Allowed CIDRS WAN, multiple values from flags",
			args: []string{`-data-dir=` + dataDir, `-serf-wan-allowed-cidrs=192.168.4.0/24`, `-serf-wan-allowed-cidrs=192.168.3.0/24`},
			json: []string{},
			hcl:  []string{},
			patch: func(rt *RuntimeConfig) {
				rt.DataDir = dataDir
				rt.SerfAllowedCIDRsWAN = []net.IPNet{*(parseCIDR(t, "192.168.4.0/24")), *(parseCIDR(t, "192.168.3.0/24"))}
			},
		},

		// ------------------------------------------------------------
		// validations
		//

		{
			desc: "invalid input",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`this is not JSON`},
			hcl:  []string{`*** 0123 this is not HCL`},
			err:  "failed to parse",
		},
		{
			desc: "datacenter is lower-cased",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{ "datacenter": "A" }`},
			hcl:  []string{`datacenter = "A"`},
			patch: func(rt *RuntimeConfig) {
				rt.Datacenter = "a"
				rt.ACLDatacenter = "a"
				rt.PrimaryDatacenter = "a"
				rt.DataDir = dataDir
			},
		},
		{
			desc: "acl_datacenter is lower-cased",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{ "acl_datacenter": "A" }`},
			hcl:  []string{`acl_datacenter = "A"`},
			patch: func(rt *RuntimeConfig) {
				rt.ACLsEnabled = true
				rt.ACLDatacenter = "a"
				rt.DataDir = dataDir
				rt.PrimaryDatacenter = "a"
			},
			warns: []string{`The 'acl_datacenter' field is deprecated. Use the 'primary_datacenter' field instead.`},
		},
		{
			desc: "acl_replication_token enables acl replication",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{ "acl_replication_token": "a" }`},
			hcl:  []string{`acl_replication_token = "a"`},
			patch: func(rt *RuntimeConfig) {
				rt.ACLTokens.ACLReplicationToken = "a"
				rt.ACLTokenReplication = true
				rt.DataDir = dataDir
			},
		},
		{
			desc: "acl_enforce_version_8 is deprecated",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{ "acl_enforce_version_8": true }`},
			hcl:  []string{`acl_enforce_version_8 = true`},
			patch: func(rt *RuntimeConfig) {
				rt.DataDir = dataDir
			},
			warns: []string{`config key "acl_enforce_version_8" is deprecated and should be removed`},
		},

		{
			desc: "advertise address detect fails v4",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{ "bind_addr": "0.0.0.0"}`},
			hcl:  []string{`bind_addr = "0.0.0.0"`},
			privatev4: func() ([]*net.IPAddr, error) {
				return nil, errors.New("some error")
			},
			err: "Error detecting private IPv4 address: some error",
		},
		{
			desc: "advertise address detect none v4",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{ "bind_addr": "0.0.0.0"}`},
			hcl:  []string{`bind_addr = "0.0.0.0"`},
			privatev4: func() ([]*net.IPAddr, error) {
				return nil, nil
			},
			err: "No private IPv4 address found",
		},
		{
			desc: "advertise address detect multiple v4",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{ "bind_addr": "0.0.0.0"}`},
			hcl:  []string{`bind_addr = "0.0.0.0"`},
			privatev4: func() ([]*net.IPAddr, error) {
				return []*net.IPAddr{ipAddr("1.1.1.1"), ipAddr("2.2.2.2")}, nil
			},
			err: "Multiple private IPv4 addresses found. Please configure one",
		},
		{
			desc: "advertise address detect fails v6",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{ "bind_addr": "::"}`},
			hcl:  []string{`bind_addr = "::"`},
			publicv6: func() ([]*net.IPAddr, error) {
				return nil, errors.New("some error")
			},
			err: "Error detecting public IPv6 address: some error",
		},
		{
			desc: "advertise address detect none v6",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{ "bind_addr": "::"}`},
			hcl:  []string{`bind_addr = "::"`},
			publicv6: func() ([]*net.IPAddr, error) {
				return nil, nil
			},
			err: "No public IPv6 address found",
		},
		{
			desc: "advertise address detect multiple v6",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{ "bind_addr": "::"}`},
			hcl:  []string{`bind_addr = "::"`},
			publicv6: func() ([]*net.IPAddr, error) {
				return []*net.IPAddr{ipAddr("dead:beef::1"), ipAddr("dead:beef::2")}, nil
			},
			err: "Multiple public IPv6 addresses found. Please configure one",
		},
		{
			desc:     "ae_interval invalid == 0",
			args:     []string{`-data-dir=` + dataDir},
			jsontail: []string{`{ "ae_interval": "0s" }`},
			hcltail:  []string{`ae_interval = "0s"`},
			err:      `ae_interval cannot be 0s. Must be positive`,
		},
		{
			desc:     "ae_interval invalid < 0",
			args:     []string{`-data-dir=` + dataDir},
			jsontail: []string{`{ "ae_interval": "-1s" }`},
			hcltail:  []string{`ae_interval = "-1s"`},
			err:      `ae_interval cannot be -1s. Must be positive`,
		},
		{
			desc: "acl_datacenter invalid",
			args: []string{
				`-datacenter=a`,
				`-data-dir=` + dataDir,
			},
			json:  []string{`{ "acl_datacenter": "%" }`},
			hcl:   []string{`acl_datacenter = "%"`},
			err:   `acl_datacenter can only contain lowercase alphanumeric, - or _ characters.`,
			warns: []string{`The 'acl_datacenter' field is deprecated. Use the 'primary_datacenter' field instead.`},
		},
		{
			desc: "autopilot.max_trailing_logs invalid",
			args: []string{
				`-datacenter=a`,
				`-data-dir=` + dataDir,
			},
			json: []string{`{ "autopilot": { "max_trailing_logs": -1 } }`},
			hcl:  []string{`autopilot = { max_trailing_logs = -1 }`},
			err:  "autopilot.max_trailing_logs cannot be -1. Must be greater than or equal to zero",
		},
		{
			desc: "bind_addr cannot be empty",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{ "bind_addr": "" }`},
			hcl:  []string{`bind_addr = ""`},
			err:  "bind_addr cannot be empty",
		},
		{
			desc: "bind_addr does not allow multiple addresses",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{ "bind_addr": "1.1.1.1 2.2.2.2" }`},
			hcl:  []string{`bind_addr = "1.1.1.1 2.2.2.2"`},
			err:  "bind_addr cannot contain multiple addresses",
		},
		{
			desc: "bind_addr cannot be a unix socket",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{ "bind_addr": "unix:///foo" }`},
			hcl:  []string{`bind_addr = "unix:///foo"`},
			err:  "bind_addr cannot be a unix socket",
		},
		{
			desc: "bootstrap without server",
			args: []string{
				`-datacenter=a`,
				`-data-dir=` + dataDir,
			},
			json: []string{`{ "bootstrap": true }`},
			hcl:  []string{`bootstrap = true`},
			err:  "'bootstrap = true' requires 'server = true'",
		},
		{
			desc: "bootstrap-expect without server",
			args: []string{
				`-datacenter=a`,
				`-data-dir=` + dataDir,
			},
			json: []string{`{ "bootstrap_expect": 3 }`},
			hcl:  []string{`bootstrap_expect = 3`},
			err:  "'bootstrap_expect > 0' requires 'server = true'",
		},
		{
			desc: "bootstrap-expect invalid",
			args: []string{
				`-datacenter=a`,
				`-data-dir=` + dataDir,
			},
			json: []string{`{ "bootstrap_expect": -1 }`},
			hcl:  []string{`bootstrap_expect = -1`},
			err:  "bootstrap_expect cannot be -1. Must be greater than or equal to zero",
		},
		{
			desc: "bootstrap-expect and dev mode",
			args: []string{
				`-dev`,
				`-datacenter=a`,
				`-data-dir=` + dataDir,
			},
			json: []string{`{ "bootstrap_expect": 3, "server": true }`},
			hcl:  []string{`bootstrap_expect = 3 server = true`},
			err:  "'bootstrap_expect > 0' not allowed in dev mode",
		},
		{
			desc: "bootstrap-expect and bootstrap",
			args: []string{
				`-datacenter=a`,
				`-data-dir=` + dataDir,
			},
			json: []string{`{ "bootstrap": true, "bootstrap_expect": 3, "server": true }`},
			hcl:  []string{`bootstrap = true bootstrap_expect = 3 server = true`},
			err:  "'bootstrap_expect > 0' and 'bootstrap = true' are mutually exclusive",
		},
		{
			desc: "bootstrap-expect=1 equals bootstrap",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{ "bootstrap_expect": 1, "server": true }`},
			hcl:  []string{`bootstrap_expect = 1 server = true`},
			patch: func(rt *RuntimeConfig) {
				rt.Bootstrap = true
				rt.BootstrapExpect = 0
				rt.LeaveOnTerm = false
				rt.ServerMode = true
				rt.SkipLeaveOnInt = true
				rt.DataDir = dataDir
			},
			warns: []string{"BootstrapExpect is set to 1; this is the same as Bootstrap mode.", "bootstrap = true: do not enable unless necessary"},
		},
		{
			desc: "bootstrap-expect=2 warning",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{ "bootstrap_expect": 2, "server": true }`},
			hcl:  []string{`bootstrap_expect = 2 server = true`},
			patch: func(rt *RuntimeConfig) {
				rt.BootstrapExpect = 2
				rt.LeaveOnTerm = false
				rt.ServerMode = true
				rt.SkipLeaveOnInt = true
				rt.DataDir = dataDir
			},
			warns: []string{
				`bootstrap_expect = 2: A cluster with 2 servers will provide no failure tolerance. See https://www.consul.io/docs/internals/consensus.html#deployment-table`,
				`bootstrap_expect > 0: expecting 2 servers`,
			},
		},
		{
			desc: "bootstrap-expect > 2 but even warning",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{ "bootstrap_expect": 4, "server": true }`},
			hcl:  []string{`bootstrap_expect = 4 server = true`},
			patch: func(rt *RuntimeConfig) {
				rt.BootstrapExpect = 4
				rt.LeaveOnTerm = false
				rt.ServerMode = true
				rt.SkipLeaveOnInt = true
				rt.DataDir = dataDir
			},
			warns: []string{
				`bootstrap_expect is even number: A cluster with an even number of servers does not achieve optimum fault tolerance. See https://www.consul.io/docs/internals/consensus.html#deployment-table`,
				`bootstrap_expect > 0: expecting 4 servers`,
			},
		},
		{
			desc: "client mode sets LeaveOnTerm and SkipLeaveOnInt correctly",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{ "server": false }`},
			hcl:  []string{` server = false`},
			patch: func(rt *RuntimeConfig) {
				rt.LeaveOnTerm = true
				rt.ServerMode = false
				rt.SkipLeaveOnInt = false
				rt.DataDir = dataDir
			},
		},
		{
			desc: "client does not allow socket",
			args: []string{
				`-datacenter=a`,
				`-data-dir=` + dataDir,
			},
			json: []string{`{ "client_addr": "unix:///foo" }`},
			hcl:  []string{`client_addr = "unix:///foo"`},
			err:  "client_addr cannot be a unix socket",
		},
		{
			desc: "datacenter invalid",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{ "datacenter": "%" }`},
			hcl:  []string{`datacenter = "%"`},
			err:  `datacenter can only contain lowercase alphanumeric, - or _ characters.`,
		},
		{
			desc: "dns does not allow socket",
			args: []string{
				`-datacenter=a`,
				`-data-dir=` + dataDir,
			},
			json: []string{`{ "addresses": {"dns": "unix:///foo" } }`},
			hcl:  []string{`addresses = { dns = "unix:///foo" }`},
			err:  "DNS address cannot be a unix socket",
		},
		{
			desc: "ui enabled and dir specified",
			args: []string{
				`-datacenter=a`,
				`-data-dir=` + dataDir,
			},
			json: []string{`{ "ui_config": { "enabled": true, "dir": "a" } }`},
			hcl:  []string{`ui_config { enabled = true dir = "a"}`},
			err: "Both the ui_config.enabled and ui_config.dir (or -ui and -ui-dir) were specified, please provide only one.\n" +
				"If trying to use your own web UI resources, use ui_config.dir or the -ui-dir flag.\n" +
				"The web UI is included in the binary so use ui_config.enabled or the -ui flag to enable it",
		},

		// test ANY address failures
		// to avoid combinatory explosion for tests use 0.0.0.0, :: or [::] but not all of them
		{
			desc: "advertise_addr any",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{ "advertise_addr": "0.0.0.0" }`},
			hcl:  []string{`advertise_addr = "0.0.0.0"`},
			err:  "Advertise address cannot be 0.0.0.0, :: or [::]",
		},
		{
			desc: "advertise_addr_wan any",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{ "advertise_addr_wan": "::" }`},
			hcl:  []string{`advertise_addr_wan = "::"`},
			err:  "Advertise WAN address cannot be 0.0.0.0, :: or [::]",
		},
		{
			desc: "recursors any",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{ "recursors": ["::"] }`},
			hcl:  []string{`recursors = ["::"]`},
			err:  "DNS recursor address cannot be 0.0.0.0, :: or [::]",
		},
		{
			desc: "dns_config.udp_answer_limit invalid",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{ "dns_config": { "udp_answer_limit": -1 } }`},
			hcl:  []string{`dns_config = { udp_answer_limit = -1 }`},
			err:  "dns_config.udp_answer_limit cannot be -1. Must be greater than or equal to zero",
		},
		{
			desc: "dns_config.a_record_limit invalid",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{ "dns_config": { "a_record_limit": -1 } }`},
			hcl:  []string{`dns_config = { a_record_limit = -1 }`},
			err:  "dns_config.a_record_limit cannot be -1. Must be greater than or equal to zero",
		},
		{
			desc: "performance.raft_multiplier < 0",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{ "performance": { "raft_multiplier": -1 } }`},
			hcl:  []string{`performance = { raft_multiplier = -1 }`},
			err:  `performance.raft_multiplier cannot be -1. Must be between 1 and 10`,
		},
		{
			desc: "performance.raft_multiplier == 0",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{ "performance": { "raft_multiplier": 0 } }`},
			hcl:  []string{`performance = { raft_multiplier = 0 }`},
			err:  `performance.raft_multiplier cannot be 0. Must be between 1 and 10`,
		},
		{
			desc: "performance.raft_multiplier > 10",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{ "performance": { "raft_multiplier": 20 } }`},
			hcl:  []string{`performance = { raft_multiplier = 20 }`},
			err:  `performance.raft_multiplier cannot be 20. Must be between 1 and 10`,
		},
		{
			desc: "node_name invalid",
			args: []string{
				`-data-dir=` + dataDir,
				`-node=`,
			},
			hostname: func() (string, error) { return "", nil },
			err:      "node_name cannot be empty",
		},
		{
			desc: "node_meta key too long",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{
				`{ "dns_config": { "udp_answer_limit": 1 } }`,
				`{ "node_meta": { "` + randomString(130) + `": "a" } }`,
			},
			hcl: []string{
				`dns_config = { udp_answer_limit = 1 }`,
				`node_meta = { "` + randomString(130) + `" = "a" }`,
			},
			err: "Key is too long (limit: 128 characters)",
		},
		{
			desc: "node_meta value too long",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{
				`{ "dns_config": { "udp_answer_limit": 1 } }`,
				`{ "node_meta": { "a": "` + randomString(520) + `" } }`,
			},
			hcl: []string{
				`dns_config = { udp_answer_limit = 1 }`,
				`node_meta = { "a" = "` + randomString(520) + `" }`,
			},
			err: "Value is too long (limit: 512 characters)",
		},
		{
			desc: "node_meta too many keys",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{
				`{ "dns_config": { "udp_answer_limit": 1 } }`,
				`{ "node_meta": {` + metaPairs(70, "json") + `} }`,
			},
			hcl: []string{
				`dns_config = { udp_answer_limit = 1 }`,
				`node_meta = {` + metaPairs(70, "hcl") + ` }`,
			},
			err: "Node metadata cannot contain more than 64 key/value pairs",
		},
		{
			desc: "unique listeners dns vs http",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{
					"client_addr": "1.2.3.4",
					"ports": { "dns": 1000, "http": 1000 }
				}`},
			hcl: []string{`
					client_addr = "1.2.3.4"
					ports = { dns = 1000 http = 1000 }
				`},
			err: "HTTP address 1.2.3.4:1000 already configured for DNS",
		},
		{
			desc: "unique listeners dns vs https",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{
					"client_addr": "1.2.3.4",
					"ports": { "dns": 1000, "https": 1000 }
				}`},
			hcl: []string{`
					client_addr = "1.2.3.4"
					ports = { dns = 1000 https = 1000 }
				`},
			err: "HTTPS address 1.2.3.4:1000 already configured for DNS",
		},
		{
			desc: "unique listeners http vs https",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{
					"client_addr": "1.2.3.4",
					"ports": { "http": 1000, "https": 1000 }
				}`},
			hcl: []string{`
					client_addr = "1.2.3.4"
					ports = { http = 1000 https = 1000 }
				`},
			err: "HTTPS address 1.2.3.4:1000 already configured for HTTP",
		},
		{
			desc: "unique advertise addresses HTTP vs RPC",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{
					"addresses": { "http": "10.0.0.1" },
					"ports": { "http": 1000, "server": 1000 }
				}`},
			hcl: []string{`
					addresses = { http = "10.0.0.1" }
					ports = { http = 1000 server = 1000 }
				`},
			err: "RPC Advertise address 10.0.0.1:1000 already configured for HTTP",
		},
		{
			desc: "unique advertise addresses RPC vs Serf LAN",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{
					"ports": { "server": 1000, "serf_lan": 1000 }
				}`},
			hcl: []string{`
					ports = { server = 1000 serf_lan = 1000 }
				`},
			err: "Serf Advertise LAN address 10.0.0.1:1000 already configured for RPC Advertise",
		},
		{
			desc: "unique advertise addresses RPC vs Serf WAN",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{
					"ports": { "server": 1000, "serf_wan": 1000 }
				}`},
			hcl: []string{`
					ports = { server = 1000 serf_wan = 1000 }
				`},
			err: "Serf Advertise WAN address 10.0.0.1:1000 already configured for RPC Advertise",
		},
		{
			desc: "http use_cache defaults to true",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{
				"http_config": {}
			}`},
			hcl: []string{`
				http_config = {}
			`},
			patch: func(rt *RuntimeConfig) {
				rt.DataDir = dataDir
				rt.HTTPUseCache = true
			},
		},
		{
			desc: "http use_cache is enabled when true",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{
				"http_config": { "use_cache": true }
			}`},
			hcl: []string{`
				http_config = { use_cache = true }
			`},
			patch: func(rt *RuntimeConfig) {
				rt.DataDir = dataDir
				rt.HTTPUseCache = true
			},
		},
		{
			desc: "http use_cache is disabled when false",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{
				"http_config": { "use_cache": false }
			}`},
			hcl: []string{`
				http_config = { use_cache = false }
			`},
			patch: func(rt *RuntimeConfig) {
				rt.DataDir = dataDir
				rt.HTTPUseCache = false
			},
		},
		{
			desc: "sidecar_service can't have ID",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{
				  "service": {
						"name": "web",
						"port": 1234,
						"connect": {
							"sidecar_service": {
								"ID": "random-sidecar-id"
							}
						}
					}
				}`},
			hcl: []string{`
				service {
					name = "web"
					port = 1234
					connect {
						sidecar_service {
							ID = "random-sidecar-id"
						}
					}
				}
			`},
			err: "sidecar_service can't specify an ID",
		},
		{
			desc: "sidecar_service can't have nested sidecar",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{
				  "service": {
						"name": "web",
						"port": 1234,
						"connect": {
							"sidecar_service": {
								"connect": {
									"sidecar_service": {}
								}
							}
						}
					}
				}`},
			hcl: []string{`
				service {
					name = "web"
					port = 1234
					connect {
						sidecar_service {
							connect {
								sidecar_service {
								}
							}
						}
					}
				}
			`},
			err: "sidecar_service can't have a nested sidecar_service",
		},
		{
			desc: "telemetry.prefix_filter cannot be empty",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{
					"telemetry": { "prefix_filter": [""] }
				}`},
			hcl: []string{`
					telemetry = { prefix_filter = [""] }
				`},
			patch: func(rt *RuntimeConfig) {
				rt.DataDir = dataDir
			},
			warns: []string{"Cannot have empty filter rule in prefix_filter"},
		},
		{
			desc: "telemetry.prefix_filter must start with + or -",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{
					"telemetry": { "prefix_filter": ["+foo", "-bar", "nix"] }
				}`},
			hcl: []string{`
					telemetry = { prefix_filter = ["+foo", "-bar", "nix"] }
				`},
			patch: func(rt *RuntimeConfig) {
				rt.DataDir = dataDir
				rt.Telemetry.AllowedPrefixes = []string{"foo"}
				rt.Telemetry.BlockedPrefixes = []string{"bar"}
			},
			warns: []string{`Filter rule must begin with either '+' or '-': "nix"`},
		},
		{
			desc: "encrypt has invalid key",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{ "encrypt": "this is not a valid key" }`},
			hcl:  []string{` encrypt = "this is not a valid key" `},
			err:  "encrypt has invalid key: illegal base64 data at input byte 4",
		},
		{
			desc: "multiple check files",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{
				`{ "check": { "name": "a", "args": ["/bin/true"] } }`,
				`{ "check": { "name": "b", "args": ["/bin/false"] } }`,
			},
			hcl: []string{
				`check = { name = "a" args = ["/bin/true"] }`,
				`check = { name = "b" args = ["/bin/false"] }`,
			},
			patch: func(rt *RuntimeConfig) {
				rt.Checks = []*structs.CheckDefinition{
					{Name: "a", ScriptArgs: []string{"/bin/true"}, OutputMaxSize: checks.DefaultBufSize},
					{Name: "b", ScriptArgs: []string{"/bin/false"}, OutputMaxSize: checks.DefaultBufSize},
				}
				rt.DataDir = dataDir
			},
		},
		{
			desc: "grpc check",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{
				`{ "check": { "name": "a", "grpc": "localhost:12345/foo", "grpc_use_tls": true } }`,
			},
			hcl: []string{
				`check = { name = "a" grpc = "localhost:12345/foo", grpc_use_tls = true }`,
			},
			patch: func(rt *RuntimeConfig) {
				rt.Checks = []*structs.CheckDefinition{
					{Name: "a", GRPC: "localhost:12345/foo", GRPCUseTLS: true, OutputMaxSize: checks.DefaultBufSize},
				}
				rt.DataDir = dataDir
			},
		},
		{
			desc: "alias check with no node",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{
				`{ "check": { "name": "a", "alias_service": "foo" } }`,
			},
			hcl: []string{
				`check = { name = "a", alias_service = "foo" }`,
			},
			patch: func(rt *RuntimeConfig) {
				rt.Checks = []*structs.CheckDefinition{
					{Name: "a", AliasService: "foo", OutputMaxSize: checks.DefaultBufSize},
				}
				rt.DataDir = dataDir
			},
		},
		{
			desc: "multiple service files",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{
				`{ "service": { "name": "a", "port": 80 } }`,
				`{ "service": { "name": "b", "port": 90, "meta": {"my": "value"}, "weights": {"passing": 13} } }`,
			},
			hcl: []string{
				`service = { name = "a" port = 80 }`,
				`service = { name = "b" port = 90 meta={my="value"}, weights={passing=13}}`,
			},
			patch: func(rt *RuntimeConfig) {
				rt.Services = []*structs.ServiceDefinition{
					{
						Name: "a",
						Port: 80,
						Weights: &structs.Weights{
							Passing: 1,
							Warning: 1,
						},
					},
					{
						Name: "b",
						Port: 90,
						Meta: map[string]string{"my": "value"},
						Weights: &structs.Weights{
							Passing: 13,
							Warning: 1,
						},
					},
				}
				rt.DataDir = dataDir
			},
		},
		{
			desc: "service with wrong meta: too long key",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{
				`{ "service": { "name": "a", "port": 80, "meta": { "` + randomString(520) + `": "metaValue" } } }`,
			},
			hcl: []string{
				`service = { name = "a" port = 80, meta={` + randomString(520) + `="metaValue"} }`,
			},
			err: `Key is too long`,
		},
		{
			desc: "service with wrong meta: too long value",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{
				`{ "service": { "name": "a", "port": 80, "meta": { "a": "` + randomString(520) + `" } } }`,
			},
			hcl: []string{
				`service = { name = "a" port = 80, meta={a="` + randomString(520) + `"} }`,
			},
			err: `Value is too long`,
		},
		{
			desc: "service with wrong meta: too many meta",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{
				`{ "service": { "name": "a", "port": 80, "meta": { ` + metaPairs(70, "json") + `} } }`,
			},
			hcl: []string{
				`service = { name = "a" port = 80 meta={` + metaPairs(70, "hcl") + `} }`,
			},
			err: `invalid meta for service a: Node metadata cannot contain more than 64 key`,
		},
		{
			desc: "translated keys",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{
				`{
					"service": {
						"name": "a",
						"port": 80,
						"tagged_addresses": {
							"wan": {
								"address": "198.18.3.4",
								"port": 443
							}
						},
						"enable_tag_override": true,
						"check": {
							"id": "x",
							"name": "y",
							"DockerContainerID": "z",
							"DeregisterCriticalServiceAfter": "10s",
							"ScriptArgs": ["a", "b"]
						}
					}
				}`,
			},
			hcl: []string{
				`service = {
					name = "a"
					port = 80
					enable_tag_override = true
					tagged_addresses = {
						wan = {
							address = "198.18.3.4"
							port = 443
						}
					}
					check = {
						id = "x"
						name = "y"
						DockerContainerID = "z"
						DeregisterCriticalServiceAfter = "10s"
						ScriptArgs = ["a", "b"]
					}
				}`,
			},
			patch: func(rt *RuntimeConfig) {
				rt.Services = []*structs.ServiceDefinition{
					{
						Name: "a",
						Port: 80,
						TaggedAddresses: map[string]structs.ServiceAddress{
							"wan": {
								Address: "198.18.3.4",
								Port:    443,
							},
						},
						EnableTagOverride: true,
						Checks: []*structs.CheckType{
							{
								CheckID:                        types.CheckID("x"),
								Name:                           "y",
								DockerContainerID:              "z",
								DeregisterCriticalServiceAfter: 10 * time.Second,
								ScriptArgs:                     []string{"a", "b"},
								OutputMaxSize:                  checks.DefaultBufSize,
							},
						},
						Weights: &structs.Weights{
							Passing: 1,
							Warning: 1,
						},
					},
				}
				rt.DataDir = dataDir
			},
		},
		{
			desc: "ignore snapshot_agent sub-object",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{
				`{ "snapshot_agent": { "dont": "care" } }`,
			},
			hcl: []string{
				`snapshot_agent = { dont = "care" }`,
			},
			patch: func(rt *RuntimeConfig) {
				rt.DataDir = dataDir
			},
		},

		{
			// Test that slices in structured config are preserved by
			// decode.HookWeakDecodeFromSlice.
			desc: "service.connectsidecar_service with checks and upstreams",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{
				  "service": {
						"name": "web",
						"port": 1234,
						"connect": {
							"sidecar_service": {
								"port": 2345,
								"checks": [
									{
										"TCP": "127.0.0.1:2345",
										"Interval": "10s"
									}
								],
								"proxy": {
									"expose": {
										"checks": true,
										"paths": [
											{
												"path": "/health",
												"local_path_port": 8080,
												"listener_port": 21500,
												"protocol": "http"
											}
										]
									},
									"upstreams": [
										{
											"destination_name": "db",
											"local_bind_port": 7000
										}
									]
								}
							}
						}
					}
				}`},
			hcl: []string{`
				service {
					name = "web"
					port = 1234
					connect {
						sidecar_service {
							port = 2345
							checks = [
								{
									tcp = "127.0.0.1:2345"
									interval = "10s"
								}
							]
							proxy {
								expose {
									checks = true
									paths = [
										{
											path = "/health"
											local_path_port = 8080
											listener_port = 21500
											protocol = "http"
										}
									]
								},
								upstreams = [
									{
										destination_name = "db"
										local_bind_port = 7000
									},
								]
							}
						}
					}
				}
			`},
			patch: func(rt *RuntimeConfig) {
				rt.DataDir = dataDir
				rt.Services = []*structs.ServiceDefinition{
					{
						Name: "web",
						Port: 1234,
						Connect: &structs.ServiceConnect{
							SidecarService: &structs.ServiceDefinition{
								Port: 2345,
								Checks: structs.CheckTypes{
									{
										TCP:           "127.0.0.1:2345",
										Interval:      10 * time.Second,
										OutputMaxSize: checks.DefaultBufSize,
									},
								},
								Proxy: &structs.ConnectProxyConfig{
									Expose: structs.ExposeConfig{
										Checks: true,
										Paths: []structs.ExposePath{
											{
												Path:          "/health",
												LocalPathPort: 8080,
												ListenerPort:  21500,
												Protocol:      "http",
											},
										},
									},
									Upstreams: structs.Upstreams{
										structs.Upstream{
											DestinationType: "service",
											DestinationName: "db",
											LocalBindPort:   7000,
										},
									},
								},
								Weights: &structs.Weights{
									Passing: 1,
									Warning: 1,
								},
							},
						},
						Weights: &structs.Weights{
							Passing: 1,
							Warning: 1,
						},
					},
				}
			},
		},
		{
			// Test that slices in structured config are preserved by
			// decode.HookWeakDecodeFromSlice.
			desc: "services.connect.sidecar_service with checks and upstreams",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{
				  "services": [{
						"name": "web",
						"port": 1234,
						"connect": {
							"sidecar_service": {
								"port": 2345,
								"checks": [
									{
										"TCP": "127.0.0.1:2345",
										"Interval": "10s"
									}
								],
								"proxy": {
									"expose": {
										"checks": true,
										"paths": [
											{
												"path": "/health",
												"local_path_port": 8080,
												"listener_port": 21500,
												"protocol": "http"
											}
										]
									},
									"upstreams": [
										{
											"destination_name": "db",
											"local_bind_port": 7000
										}
									]
								}
							}
						}
					}]
				}`},
			hcl: []string{`
				services = [{
					name = "web"
					port = 1234
					connect {
						sidecar_service {
							port = 2345
							checks = [
								{
									tcp = "127.0.0.1:2345"
									interval = "10s"
								}
							]
							proxy {
								expose {
									checks = true
									paths = [
										{
											path = "/health"
											local_path_port = 8080
											listener_port = 21500
											protocol = "http"
										}
									]
								},
								upstreams = [
									{
										destination_name = "db"
										local_bind_port = 7000
									},
								]
							}
						}
					}
				}]
			`},
			patch: func(rt *RuntimeConfig) {
				rt.DataDir = dataDir
				rt.Services = []*structs.ServiceDefinition{
					{
						Name: "web",
						Port: 1234,
						Connect: &structs.ServiceConnect{
							SidecarService: &structs.ServiceDefinition{
								Port: 2345,
								Checks: structs.CheckTypes{
									{
										TCP:           "127.0.0.1:2345",
										Interval:      10 * time.Second,
										OutputMaxSize: checks.DefaultBufSize,
									},
								},
								Proxy: &structs.ConnectProxyConfig{
									Expose: structs.ExposeConfig{
										Checks: true,
										Paths: []structs.ExposePath{
											{
												Path:          "/health",
												LocalPathPort: 8080,
												ListenerPort:  21500,
												Protocol:      "http",
											},
										},
									},
									Upstreams: structs.Upstreams{
										structs.Upstream{
											DestinationType: "service",
											DestinationName: "db",
											LocalBindPort:   7000,
										},
									},
								},
								Weights: &structs.Weights{
									Passing: 1,
									Warning: 1,
								},
							},
						},
						Weights: &structs.Weights{
							Passing: 1,
							Warning: 1,
						},
					},
				}
			},
		},
		{
			// This tests checks that VerifyServerHostname implies VerifyOutgoing
			desc: "verify_server_hostname implies verify_outgoing",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{
			  "verify_server_hostname": true
			}`},
			hcl: []string{`
			  verify_server_hostname = true
			`},
			patch: func(rt *RuntimeConfig) {
				rt.DataDir = dataDir
				rt.VerifyServerHostname = true
				rt.VerifyOutgoing = true
			},
		},
		{
			desc: "auto_encrypt.allow_tls works implies connect",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{
			  "verify_incoming": true,
			  "auto_encrypt": { "allow_tls": true },
			  "server": true
			}`},
			hcl: []string{`
			  verify_incoming = true
			  auto_encrypt { allow_tls = true }
			  server = true
			`},
			patch: func(rt *RuntimeConfig) {
				rt.DataDir = dataDir
				rt.VerifyIncoming = true
				rt.AutoEncryptAllowTLS = true
				rt.ConnectEnabled = true

				// server things
				rt.ServerMode = true
				rt.LeaveOnTerm = false
				rt.SkipLeaveOnInt = true
			},
		},
		{
			desc: "auto_encrypt.allow_tls works with verify_incoming",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{
			  "verify_incoming": true,
			  "auto_encrypt": { "allow_tls": true },
			  "server": true
			}`},
			hcl: []string{`
			  verify_incoming = true
			  auto_encrypt { allow_tls = true }
			  server = true
			`},
			patch: func(rt *RuntimeConfig) {
				rt.DataDir = dataDir
				rt.VerifyIncoming = true
				rt.AutoEncryptAllowTLS = true
				rt.ConnectEnabled = true

				// server things
				rt.ServerMode = true
				rt.LeaveOnTerm = false
				rt.SkipLeaveOnInt = true
			},
		},
		{
			desc: "auto_encrypt.allow_tls works with verify_incoming_rpc",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{
			  "verify_incoming_rpc": true,
			  "auto_encrypt": { "allow_tls": true },
			  "server": true
			}`},
			hcl: []string{`
			  verify_incoming_rpc = true
			  auto_encrypt { allow_tls = true }
			  server = true
			`},
			patch: func(rt *RuntimeConfig) {
				rt.DataDir = dataDir
				rt.VerifyIncomingRPC = true
				rt.AutoEncryptAllowTLS = true
				rt.ConnectEnabled = true

				// server things
				rt.ServerMode = true
				rt.LeaveOnTerm = false
				rt.SkipLeaveOnInt = true
			},
		},
		{
			desc: "auto_encrypt.allow_tls warns without verify_incoming or verify_incoming_rpc",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{
			  "auto_encrypt": { "allow_tls": true },
			  "server": true
			}`},
			hcl: []string{`
			  auto_encrypt { allow_tls = true }
			  server = true
			`},
			warns: []string{"if auto_encrypt.allow_tls is turned on, either verify_incoming or verify_incoming_rpc should be enabled. It is necessary to turn it off during a migration to TLS, but it should definitely be turned on afterwards."},
			patch: func(rt *RuntimeConfig) {
				rt.DataDir = dataDir
				rt.AutoEncryptAllowTLS = true
				rt.ConnectEnabled = true
				// server things
				rt.ServerMode = true
				rt.LeaveOnTerm = false
				rt.SkipLeaveOnInt = true
			},
		},
		{
			desc: "auto_encrypt.allow_tls errors in client mode",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{
			  "auto_encrypt": { "allow_tls": true },
			  "server": false
			}`},
			hcl: []string{`
			  auto_encrypt { allow_tls = true }
			  server = false
			`},
			err: "auto_encrypt.allow_tls can only be used on a server.",
		},
		{
			desc: "auto_encrypt.tls errors in server mode",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{
			  "auto_encrypt": { "tls": true },
			  "server": true
			}`},
			hcl: []string{`
			  auto_encrypt { tls = true }
			  server = true
			`},
			err: "auto_encrypt.tls can only be used on a client.",
		},
		{
			desc: "test connect vault provider configuration",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{
				"connect": {
					"enabled": true,
					"ca_provider": "vault",
					"ca_config": {
						"ca_file": "/capath/ca.pem",
						"ca_path": "/capath/",
						"cert_file": "/certpath/cert.pem",
						"key_file": "/certpath/key.pem",
						"tls_server_name": "server.name",
						"tls_skip_verify": true,
						"token": "abc",
						"root_pki_path": "consul-vault",
						"intermediate_pki_path": "connect-intermediate"
					}
				}
			}`},
			hcl: []string{`
			  connect {
					enabled = true
					ca_provider = "vault"
					ca_config {
						ca_file = "/capath/ca.pem"
						ca_path = "/capath/"
						cert_file = "/certpath/cert.pem"
						key_file = "/certpath/key.pem"
						tls_server_name = "server.name"
						tls_skip_verify = true
						token = "abc"
						root_pki_path = "consul-vault"
						intermediate_pki_path = "connect-intermediate"
					}
				}
			`},
			patch: func(rt *RuntimeConfig) {
				rt.DataDir = dataDir
				rt.ConnectEnabled = true
				rt.ConnectCAProvider = "vault"
				rt.ConnectCAConfig = map[string]interface{}{
					"CAFile":              "/capath/ca.pem",
					"CAPath":              "/capath/",
					"CertFile":            "/certpath/cert.pem",
					"KeyFile":             "/certpath/key.pem",
					"TLSServerName":       "server.name",
					"TLSSkipVerify":       true,
					"Token":               "abc",
					"RootPKIPath":         "consul-vault",
					"IntermediatePKIPath": "connect-intermediate",
				}
			},
		},
		{
			desc: "Connect AWS CA provider configuration",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{
				"connect": {
					"enabled": true,
					"ca_provider": "aws-pca",
					"ca_config": {
						"existing_arn": "foo",
						"delete_on_exit": true
					}
				}
			}`},
			hcl: []string{`
			  connect {
					enabled = true
					ca_provider = "aws-pca"
					ca_config {
						existing_arn = "foo"
						delete_on_exit = true
					}
				}
			`},
			patch: func(rt *RuntimeConfig) {
				rt.DataDir = dataDir
				rt.ConnectEnabled = true
				rt.ConnectCAProvider = "aws-pca"
				rt.ConnectCAConfig = map[string]interface{}{
					"ExistingARN":  "foo",
					"DeleteOnExit": true,
				}
			},
		},
		{
			desc: "Connect AWS CA provider TTL validation",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{
				"connect": {
					"enabled": true,
					"ca_provider": "aws-pca",
					"ca_config": {
						"leaf_cert_ttl": "1h"
					}
				}
			}`},
			hcl: []string{`
			  connect {
					enabled = true
					ca_provider = "aws-pca"
					ca_config {
						leaf_cert_ttl = "1h"
					}
				}
			`},
			err: "AWS PCA doesn't support certificates that are valid for less than 24 hours",
		},
		{
			desc: "Connect AWS CA provider EC key length validation",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{
				"connect": {
					"enabled": true,
					"ca_provider": "aws-pca",
					"ca_config": {
						"private_key_bits": 521
					}
				}
			}`},
			hcl: []string{`
			  connect {
					enabled = true
					ca_provider = "aws-pca"
					ca_config {
						private_key_bits = 521
					}
				}
			`},
			err: "AWS PCA only supports P256 EC curve",
		},
		{
			desc: "connect.enable_mesh_gateway_wan_federation requires connect.enabled",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{
			  "connect": {
				"enabled": false,
				"enable_mesh_gateway_wan_federation": true
			  }
			}`},
			hcl: []string{`
			  connect {
			    enabled = false
			    enable_mesh_gateway_wan_federation = true
			  }
			`},
			err: "'connect.enable_mesh_gateway_wan_federation=true' requires 'connect.enabled=true'",
		},
		{
			desc: "connect.enable_mesh_gateway_wan_federation cannot use -join-wan",
			args: []string{
				`-data-dir=` + dataDir,
				`-join-wan=1.2.3.4`,
			},
			json: []string{`{
			  "server": true,
			  "primary_datacenter": "one",
			  "datacenter": "one",
			  "connect": {
				"enabled": true,
				"enable_mesh_gateway_wan_federation": true
			  }
			}`},
			hcl: []string{`
			  server = true
			  primary_datacenter = "one"
			  datacenter = "one"
			  connect {
			    enabled = true
			    enable_mesh_gateway_wan_federation = true
			  }
			`},
			err: "'start_join_wan' is incompatible with 'connect.enable_mesh_gateway_wan_federation = true'",
		},
		{
			desc: "connect.enable_mesh_gateway_wan_federation cannot use -retry-join-wan",
			args: []string{
				`-data-dir=` + dataDir,
				`-retry-join-wan=1.2.3.4`,
			},
			json: []string{`{
			  "server": true,
			  "primary_datacenter": "one",
			  "datacenter": "one",
			  "connect": {
				"enabled": true,
				"enable_mesh_gateway_wan_federation": true
			  }
			}`},
			hcl: []string{`
			  server = true
			  primary_datacenter = "one"
			  datacenter = "one"
			  connect {
			    enabled = true
			    enable_mesh_gateway_wan_federation = true
			  }
			`},
			err: "'retry_join_wan' is incompatible with 'connect.enable_mesh_gateway_wan_federation = true'",
		},
		{
			desc: "connect.enable_mesh_gateway_wan_federation requires server mode",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{
			  "server": false,
			  "connect": {
				"enabled": true,
				"enable_mesh_gateway_wan_federation": true
			  }
			}`},
			hcl: []string{`
			  server = false
			  connect {
			    enabled = true
			    enable_mesh_gateway_wan_federation = true
			  }
			`},
			err: "'connect.enable_mesh_gateway_wan_federation = true' requires 'server = true'",
		},
		{
			desc: "connect.enable_mesh_gateway_wan_federation requires no slashes in node names",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{
			  "server": true,
			  "node_name": "really/why",
			  "connect": {
				"enabled": true,
				"enable_mesh_gateway_wan_federation": true
			  }
			}`},
			hcl: []string{`
			  server = true
			  node_name = "really/why"
			  connect {
			    enabled = true
			    enable_mesh_gateway_wan_federation = true
			  }
			`},
			err: "'connect.enable_mesh_gateway_wan_federation = true' requires that 'node_name' not contain '/' characters",
		},
		{
			desc: "primary_gateways requires server mode",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{
			  "server": false,
			  "primary_gateways": [ "foo.local", "bar.local" ]
			}`},
			hcl: []string{`
			  server = false
			  primary_gateways = [ "foo.local", "bar.local" ]
			`},
			err: "'primary_gateways' requires 'server = true'",
		},
		{
			desc: "primary_gateways only works in a secondary datacenter",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{
			  "server": true,
			  "primary_datacenter": "one",
			  "datacenter": "one",
			  "primary_gateways": [ "foo.local", "bar.local" ]
			}`},
			hcl: []string{`
			  server = true
			  primary_datacenter = "one"
			  datacenter = "one"
			  primary_gateways = [ "foo.local", "bar.local" ]
			`},
			err: "'primary_gateways' should only be configured in a secondary datacenter",
		},
		{
			desc: "connect.enable_mesh_gateway_wan_federation in secondary with primary_gateways configured",
			args: []string{
				`-data-dir=` + dataDir,
			},
			json: []string{`{
			  "server": true,
			  "primary_datacenter": "one",
			  "datacenter": "two",
			  "primary_gateways": [ "foo.local", "bar.local" ],
			  "connect": {
				"enabled": true,
				"enable_mesh_gateway_wan_federation": true
			  }
			}`},
			hcl: []string{`
			  server = true
			  primary_datacenter = "one"
			  datacenter = "two"
			  primary_gateways = [ "foo.local", "bar.local" ]
			  connect {
			    enabled = true
			    enable_mesh_gateway_wan_federation = true
			  }
			`},
			patch: func(rt *RuntimeConfig) {
				rt.DataDir = dataDir
				rt.Datacenter = "two"
				rt.PrimaryDatacenter = "one"
				rt.ACLDatacenter = "one"
				rt.PrimaryGateways = []string{"foo.local", "bar.local"}
				rt.ConnectEnabled = true
				rt.ConnectMeshGatewayWANFederationEnabled = true
				// server things
				rt.ServerMode = true
				rt.LeaveOnTerm = false
				rt.SkipLeaveOnInt = true
			},
		},

		// ------------------------------------------------------------
		// ConfigEntry Handling
		//
		{
			desc: "ConfigEntry bootstrap doesn't parse",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
				"config_entries": {
					"bootstrap": [
						{
							"foo": "bar"
						}
					]
				}
			}`},
			hcl: []string{`
			config_entries {
				bootstrap {
					foo = "bar"
				}
			}`},
			err: "config_entries.bootstrap[0]: Payload does not contain a kind/Kind",
		},
		{
			desc: "ConfigEntry bootstrap unknown kind",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
				"config_entries": {
					"bootstrap": [
						{
							"kind": "foo",
							"name": "bar",
							"baz": 1
						}
					]
				}
			}`},
			hcl: []string{`
			config_entries {
				bootstrap {
					kind = "foo"
					name = "bar"
					baz = 1
				}
			}`},
			err: "config_entries.bootstrap[0]: invalid config entry kind: foo",
		},
		{
			desc: "ConfigEntry bootstrap invalid service-defaults",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
				"config_entries": {
					"bootstrap": [
						{
							"kind": "service-defaults",
							"name": "web",
							"made_up_key": "blah"
						}
					]
				}
			}`},
			hcl: []string{`
			config_entries {
				bootstrap {
					kind = "service-defaults"
					name = "web"
					made_up_key = "blah"
				}
			}`},
			err: "config_entries.bootstrap[0]: 1 error occurred:\n\t* invalid config key \"made_up_key\"\n\n",
		},
		{
			desc: "ConfigEntry bootstrap proxy-defaults (snake-case)",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
				"config_entries": {
					"bootstrap": [
						{
							"kind": "proxy-defaults",
							"name": "global",
							"config": {
								"bar": "abc",
								"moreconfig": {
									"moar": "config"
								}
							},
							"mesh_gateway": {
								"mode": "remote"
							}
						}
					]
				}
			}`},
			hcl: []string{`
				config_entries {
					bootstrap {
						kind = "proxy-defaults"
						name = "global"
						config {
						  "bar" = "abc"
						  "moreconfig" {
							"moar" = "config"
						  }
						}
						mesh_gateway {
							mode = "remote"
						}
					}
				}`},
			patch: func(rt *RuntimeConfig) {
				rt.DataDir = dataDir
				rt.ConfigEntryBootstrap = []structs.ConfigEntry{
					&structs.ProxyConfigEntry{
						Kind:           structs.ProxyDefaults,
						Name:           structs.ProxyConfigGlobal,
						EnterpriseMeta: *defaultEntMeta,
						Config: map[string]interface{}{
							"bar": "abc",
							"moreconfig": map[string]interface{}{
								"moar": "config",
							},
						},
						MeshGateway: structs.MeshGatewayConfig{
							Mode: structs.MeshGatewayModeRemote,
						},
					},
				}
			},
		},
		{
			desc: "ConfigEntry bootstrap proxy-defaults (camel-case)",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
				"config_entries": {
					"bootstrap": [
						{
							"Kind": "proxy-defaults",
							"Name": "global",
							"Config": {
								"bar": "abc",
								"moreconfig": {
									"moar": "config"
								}
							},
							"MeshGateway": {
								"Mode": "remote"
							}
						}
					]
				}
			}`},
			hcl: []string{`
				config_entries {
					bootstrap {
						Kind = "proxy-defaults"
						Name = "global"
						Config {
						  "bar" = "abc"
						  "moreconfig" {
							"moar" = "config"
						  }
						}
						MeshGateway {
							Mode = "remote"
						}
					}
				}`},
			patch: func(rt *RuntimeConfig) {
				rt.DataDir = dataDir
				rt.ConfigEntryBootstrap = []structs.ConfigEntry{
					&structs.ProxyConfigEntry{
						Kind:           structs.ProxyDefaults,
						Name:           structs.ProxyConfigGlobal,
						EnterpriseMeta: *defaultEntMeta,
						Config: map[string]interface{}{
							"bar": "abc",
							"moreconfig": map[string]interface{}{
								"moar": "config",
							},
						},
						MeshGateway: structs.MeshGatewayConfig{
							Mode: structs.MeshGatewayModeRemote,
						},
					},
				}
			},
		},
		{
			desc: "ConfigEntry bootstrap service-defaults (snake-case)",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
				"config_entries": {
					"bootstrap": [
						{
							"kind": "service-defaults",
							"name": "web",
							"meta" : {
								"foo": "bar",
								"gir": "zim"
							},
							"protocol": "http",
							"external_sni": "abc-123",
							"mesh_gateway": {
								"mode": "remote"
							}
						}
					]
				}
			}`},
			hcl: []string{`
				config_entries {
					bootstrap {
						kind = "service-defaults"
						name = "web"
						meta {
							"foo" = "bar"
							"gir" = "zim"
						}
						protocol = "http"
						external_sni = "abc-123"
						mesh_gateway {
							mode = "remote"
						}
					}
				}`},
			patch: func(rt *RuntimeConfig) {
				rt.DataDir = dataDir
				rt.ConfigEntryBootstrap = []structs.ConfigEntry{
					&structs.ServiceConfigEntry{
						Kind: structs.ServiceDefaults,
						Name: "web",
						Meta: map[string]string{
							"foo": "bar",
							"gir": "zim",
						},
						EnterpriseMeta: *defaultEntMeta,
						Protocol:       "http",
						ExternalSNI:    "abc-123",
						MeshGateway: structs.MeshGatewayConfig{
							Mode: structs.MeshGatewayModeRemote,
						},
					},
				}
			},
		},
		{
			desc: "ConfigEntry bootstrap service-defaults (camel-case)",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
				"config_entries": {
					"bootstrap": [
						{
							"Kind": "service-defaults",
							"Name": "web",
							"Meta" : {
								"foo": "bar",
								"gir": "zim"
							},
							"Protocol": "http",
							"ExternalSNI": "abc-123",
							"MeshGateway": {
								"Mode": "remote"
							}
						}
					]
				}
			}`},
			hcl: []string{`
				config_entries {
					bootstrap {
						Kind = "service-defaults"
						Name = "web"
						Meta {
							"foo" = "bar"
							"gir" = "zim"
						}
						Protocol = "http"
						ExternalSNI = "abc-123"
						MeshGateway {
							Mode = "remote"
						}
					}
				}`},
			patch: func(rt *RuntimeConfig) {
				rt.DataDir = dataDir
				rt.ConfigEntryBootstrap = []structs.ConfigEntry{
					&structs.ServiceConfigEntry{
						Kind: structs.ServiceDefaults,
						Name: "web",
						Meta: map[string]string{
							"foo": "bar",
							"gir": "zim",
						},
						EnterpriseMeta: *defaultEntMeta,
						Protocol:       "http",
						ExternalSNI:    "abc-123",
						MeshGateway: structs.MeshGatewayConfig{
							Mode: structs.MeshGatewayModeRemote,
						},
					},
				}
			},
		},
		{
			desc: "ConfigEntry bootstrap service-router (snake-case)",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
				"config_entries": {
					"bootstrap": [
						{
							"kind": "service-router",
							"name": "main",
							"meta" : {
								"foo": "bar",
								"gir": "zim"
							},
							"routes": [
								{
									"match": {
										"http": {
											"path_exact": "/foo",
											"header": [
												{
													"name": "debug1",
													"present": true
												},
												{
													"name": "debug2",
													"present": true,
													"invert": true
												},
												{
													"name": "debug3",
													"exact": "1"
												},
												{
													"name": "debug4",
													"prefix": "aaa"
												},
												{
													"name": "debug5",
													"suffix": "bbb"
												},
												{
													"name": "debug6",
													"regex": "a.*z"
												}
											]
										}
									},
									"destination": {
									  "service"                 : "carrot",
									  "service_subset"          : "kale",
									  "namespace"               : "leek",
									  "prefix_rewrite"          : "/alternate",
									  "request_timeout"         : "99s",
									  "num_retries"             : 12345,
									  "retry_on_connect_failure": true,
									  "retry_on_status_codes"   : [401, 209]
									}
								},
								{
									"match": {
										"http": {
											"path_prefix": "/foo",
											"methods": [ "GET", "DELETE" ],
											"query_param": [
												{
													"name": "hack1",
													"present": true
												},
												{
													"name": "hack2",
													"exact": "1"
												},
												{
													"name": "hack3",
													"regex": "a.*z"
												}
											]
										}
									}
								},
								{
									"match": {
										"http": {
											"path_regex": "/foo"
										}
									}
								}
							]
						}
					]
				}
			}`},
			hcl: []string{`
				config_entries {
					bootstrap {
						kind = "service-router"
						name = "main"
						meta {
							"foo" = "bar"
							"gir" = "zim"
						}
						routes = [
							{
								match {
									http {
										path_exact = "/foo"
										header = [
											{
												name = "debug1"
												present = true
											},
											{
												name = "debug2"
												present = true
												invert = true
											},
											{
												name = "debug3"
												exact = "1"
											},
											{
												name = "debug4"
												prefix = "aaa"
											},
											{
												name = "debug5"
												suffix = "bbb"
											},
											{
												name = "debug6"
												regex = "a.*z"
											},
										]
									}
								}
								destination {
								  service               = "carrot"
								  service_subset         = "kale"
								  namespace             = "leek"
								  prefix_rewrite         = "/alternate"
								  request_timeout        = "99s"
								  num_retries            = 12345
								  retry_on_connect_failure = true
								  retry_on_status_codes    = [401, 209]
								}
							},
							{
								match {
									http {
										path_prefix = "/foo"
										methods = [ "GET", "DELETE" ]
										query_param = [
											{
												name = "hack1"
												present = true
											},
											{
												name = "hack2"
												exact = "1"
											},
											{
												name = "hack3"
												regex = "a.*z"
											},
										]
									}
								}
							},
							{
								match {
									http {
										path_regex = "/foo"
									}
								}
							},
						]
					}
				}`},
			patch: func(rt *RuntimeConfig) {
				rt.DataDir = dataDir
				rt.ConfigEntryBootstrap = []structs.ConfigEntry{
					&structs.ServiceRouterConfigEntry{
						Kind: structs.ServiceRouter,
						Name: "main",
						Meta: map[string]string{
							"foo": "bar",
							"gir": "zim",
						},
						EnterpriseMeta: *defaultEntMeta,
						Routes: []structs.ServiceRoute{
							{
								Match: &structs.ServiceRouteMatch{
									HTTP: &structs.ServiceRouteHTTPMatch{
										PathExact: "/foo",
										Header: []structs.ServiceRouteHTTPMatchHeader{
											{
												Name:    "debug1",
												Present: true,
											},
											{
												Name:    "debug2",
												Present: true,
												Invert:  true,
											},
											{
												Name:  "debug3",
												Exact: "1",
											},
											{
												Name:   "debug4",
												Prefix: "aaa",
											},
											{
												Name:   "debug5",
												Suffix: "bbb",
											},
											{
												Name:  "debug6",
												Regex: "a.*z",
											},
										},
									},
								},
								Destination: &structs.ServiceRouteDestination{
									Service:               "carrot",
									ServiceSubset:         "kale",
									Namespace:             "leek",
									PrefixRewrite:         "/alternate",
									RequestTimeout:        99 * time.Second,
									NumRetries:            12345,
									RetryOnConnectFailure: true,
									RetryOnStatusCodes:    []uint32{401, 209},
								},
							},
							{
								Match: &structs.ServiceRouteMatch{
									HTTP: &structs.ServiceRouteHTTPMatch{
										PathPrefix: "/foo",
										Methods:    []string{"GET", "DELETE"},
										QueryParam: []structs.ServiceRouteHTTPMatchQueryParam{
											{
												Name:    "hack1",
												Present: true,
											},
											{
												Name:  "hack2",
												Exact: "1",
											},
											{
												Name:  "hack3",
												Regex: "a.*z",
											},
										},
									},
								},
							},
							{
								Match: &structs.ServiceRouteMatch{
									HTTP: &structs.ServiceRouteHTTPMatch{
										PathRegex: "/foo",
									},
								},
							},
						},
					},
				}
			},
		},
		// TODO(rb): add in missing tests for ingress-gateway (snake + camel)
		// TODO(rb): add in missing tests for terminating-gateway (snake + camel)
		{
			desc: "ConfigEntry bootstrap service-intentions (snake-case)",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
				"config_entries": {
					"bootstrap": [
						{
							"kind": "service-intentions",
							"name": "web",
							"meta" : {
								"foo": "bar",
								"gir": "zim"
							},
							"sources": [
								{
									"name": "foo",
									"action": "deny",
									"type": "consul",
									"description": "foo desc"
								},
								{
									"name": "bar",
									"action": "allow",
									"description": "bar desc"
								},
								{
									"name": "*",
									"action": "deny",
									"description": "wild desc"
								}
							]
						}
					]
				}
			}`,
			},
			hcl: []string{`
				config_entries {
				  bootstrap {
					kind = "service-intentions"
					name = "web"
					meta {
						"foo" = "bar"
						"gir" = "zim"
					}
					sources = [
					  {
						name        = "foo"
						action      = "deny"
						type        = "consul"
						description = "foo desc"
					  },
					  {
						name        = "bar"
						action      = "allow"
						description = "bar desc"
					  }
					]
					sources {
					  name        = "*"
					  action      = "deny"
					  description = "wild desc"
					}
				  }
				}
			`,
			},
			patchActual: func(rt *RuntimeConfig) {
				// Wipe the time tracking fields to make comparison easier.
				for _, raw := range rt.ConfigEntryBootstrap {
					if entry, ok := raw.(*structs.ServiceIntentionsConfigEntry); ok {
						for _, src := range entry.Sources {
							src.LegacyCreateTime = nil
							src.LegacyUpdateTime = nil
						}
					}
				}
			},
			patch: func(rt *RuntimeConfig) {
				rt.DataDir = dataDir
				rt.ConfigEntryBootstrap = []structs.ConfigEntry{
					&structs.ServiceIntentionsConfigEntry{
						Kind: "service-intentions",
						Name: "web",
						Meta: map[string]string{
							"foo": "bar",
							"gir": "zim",
						},
						EnterpriseMeta: *defaultEntMeta,
						Sources: []*structs.SourceIntention{
							{
								Name:           "foo",
								Action:         "deny",
								Type:           "consul",
								Description:    "foo desc",
								Precedence:     9,
								EnterpriseMeta: *defaultEntMeta,
							},
							{
								Name:           "bar",
								Action:         "allow",
								Type:           "consul",
								Description:    "bar desc",
								Precedence:     9,
								EnterpriseMeta: *defaultEntMeta,
							},
							{
								Name:           "*",
								Action:         "deny",
								Type:           "consul",
								Description:    "wild desc",
								Precedence:     8,
								EnterpriseMeta: *defaultEntMeta,
							},
						},
					},
				}
			},
		},
		{
			desc: "ConfigEntry bootstrap service-intentions wildcard destination (snake-case)",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
				"config_entries": {
					"bootstrap": [
						{
							"kind": "service-intentions",
							"name": "*",
							"sources": [
								{
									"name": "foo",
									"action": "deny",
									"precedence": 6
								}
							]
						}
					]
				}
			}`,
			},
			hcl: []string{`
				config_entries {
				  bootstrap {
					kind = "service-intentions"
					name = "*"
					sources {
					  name   = "foo"
					  action = "deny"
					  # should be parsed, but we'll ignore it later
					  precedence = 6
					}
				  }
				}
			`,
			},
			patchActual: func(rt *RuntimeConfig) {
				// Wipe the time tracking fields to make comparison easier.
				for _, raw := range rt.ConfigEntryBootstrap {
					if entry, ok := raw.(*structs.ServiceIntentionsConfigEntry); ok {
						for _, src := range entry.Sources {
							src.LegacyCreateTime = nil
							src.LegacyUpdateTime = nil
						}
					}
				}
			},
			patch: func(rt *RuntimeConfig) {
				rt.DataDir = dataDir
				rt.ConfigEntryBootstrap = []structs.ConfigEntry{
					&structs.ServiceIntentionsConfigEntry{
						Kind:           "service-intentions",
						Name:           "*",
						EnterpriseMeta: *defaultEntMeta,
						Sources: []*structs.SourceIntention{
							{
								Name:           "foo",
								Action:         "deny",
								Type:           "consul",
								Precedence:     6,
								EnterpriseMeta: *defaultEntMeta,
							},
						},
					},
				}
			},
		},

		///////////////////////////////////
		// Defaults sanity checks

		{
			desc: "default limits",
			args: []string{
				`-data-dir=` + dataDir,
			},
			patch: func(rt *RuntimeConfig) {
				rt.DataDir = dataDir
				// Note that in the happy case this test will pass even if you comment
				// out all the stuff below since rt is also initialized from the
				// defaults. But it's still valuable as it will fail as soon as the
				// defaults are changed from these values forcing that change to be
				// intentional.
				rt.RPCHandshakeTimeout = 5 * time.Second
				rt.HTTPSHandshakeTimeout = 5 * time.Second
				rt.HTTPMaxConnsPerClient = 200
				rt.RPCMaxConnsPerClient = 100
			},
		},

		///////////////////////////////////
		// Auto Config related tests
		{
			desc: "auto config and auto encrypt error",
			args: []string{
				`-data-dir=` + dataDir,
			},
			hcl: []string{`
				auto_config {
					enabled = true
					intro_token = "blah"
					server_addresses = ["198.18.0.1"]
				}
				auto_encrypt {
					tls = true
				}
				verify_outgoing = true
			`},
			json: []string{`{
				"auto_config": {
					"enabled": true,
					"intro_token": "blah",
					"server_addresses": ["198.18.0.1"]
				},
				"auto_encrypt": {
					"tls": true
				},
				"verify_outgoing": true
			}`},
			err: "both auto_encrypt.tls and auto_config.enabled cannot be set to true.",
		},
		{
			desc: "auto config not allowed for servers",
			args: []string{
				`-data-dir=` + dataDir,
			},
			hcl: []string{`
				server = true
				auto_config {
					enabled = true
					intro_token = "blah"
					server_addresses = ["198.18.0.1"]
				}
				verify_outgoing = true
			`},
			json: []string{`
			{
				"server": true,
				"auto_config": {
					"enabled": true,
					"intro_token": "blah",
					"server_addresses": ["198.18.0.1"]
				},
				"verify_outgoing": true
			}`},
			err: "auto_config.enabled cannot be set to true for server agents",
		},

		{
			desc: "auto config tls not enabled",
			args: []string{
				`-data-dir=` + dataDir,
			},
			hcl: []string{`
				auto_config {
					enabled = true
					server_addresses = ["198.18.0.1"]
					intro_token = "foo" 
				}
			`},
			json: []string{`
			{
				"auto_config": {
					"enabled": true,
					"server_addresses": ["198.18.0.1"],
					"intro_token": "foo"
				}
			}`},
			err: "auto_config.enabled cannot be set without configuring TLS for server communications",
		},

		{
			desc: "auto config server tls not enabled",
			args: []string{
				`-data-dir=` + dataDir,
			},
			hcl: []string{`
				server = true
				auto_config {
					authorization {
						enabled = true
					}
				}
			`},
			json: []string{`
			{
				"server": true,
				"auto_config": {
					"authorization": {
						"enabled": true
					}
				}
			}`},
			err: "auto_config.authorization.enabled cannot be set without providing a TLS certificate for the server",
		},

		{
			desc: "auto config no intro token",
			args: []string{
				`-data-dir=` + dataDir,
			},
			hcl: []string{`
				auto_config {
					enabled = true
				 	server_addresses = ["198.18.0.1"]
				}
				verify_outgoing = true
			`},
			json: []string{`
			{
				"auto_config": {
					"enabled": true,
					"server_addresses": ["198.18.0.1"]
				},
				"verify_outgoing": true
			}`},
			err: "One of auto_config.intro_token, auto_config.intro_token_file or the CONSUL_INTRO_TOKEN environment variable must be set to enable auto_config",
		},

		{
			desc: "auto config no server addresses",
			args: []string{
				`-data-dir=` + dataDir,
			},
			hcl: []string{`
				auto_config {
					enabled = true
					intro_token = "blah"
				}
				verify_outgoing = true
			`},
			json: []string{`
			{
				"auto_config": {
					"enabled": true,
					"intro_token": "blah"
				},
				"verify_outgoing": true
			}`},
			err: "auto_config.enabled is set without providing a list of addresses",
		},

		{
			desc: "auto config client",
			args: []string{
				`-data-dir=` + dataDir,
			},
			hcl: []string{`
				auto_config {
					enabled = true
					intro_token = "blah" 
					intro_token_file = "blah"
					server_addresses = ["198.18.0.1"]
					dns_sans = ["foo"]
					ip_sans = ["invalid", "127.0.0.1"]
				}
				verify_outgoing = true
			`},
			json: []string{`
			{
				"auto_config": {
					"enabled": true,
					"intro_token": "blah",
					"intro_token_file": "blah",
					"server_addresses": ["198.18.0.1"],
					"dns_sans": ["foo"],
					"ip_sans": ["invalid", "127.0.0.1"]
				},
				"verify_outgoing": true
			}`},
			warns: []string{
				"Cannot parse ip \"invalid\" from auto_config.ip_sans",
				"Both an intro token and intro token file are set. The intro token will be used instead of the file",
			},
			patch: func(rt *RuntimeConfig) {
				rt.ConnectEnabled = true
				rt.AutoConfig.Enabled = true
				rt.AutoConfig.IntroToken = "blah"
				rt.AutoConfig.IntroTokenFile = "blah"
				rt.AutoConfig.ServerAddresses = []string{"198.18.0.1"}
				rt.AutoConfig.DNSSANs = []string{"foo"}
				rt.AutoConfig.IPSANs = []net.IP{net.IPv4(127, 0, 0, 1)}
				rt.DataDir = dataDir
				rt.VerifyOutgoing = true
			},
		},

		{
			desc: "auto config authorizer client not allowed",
			args: []string{
				`-data-dir=` + dataDir,
			},
			hcl: []string{`
				auto_config {
					authorization {
						enabled = true
					}
				}
			`},
			json: []string{`
			{
				"auto_config": {
					"authorization": {
						"enabled": true
					}
				}
			}`},
			err: "auto_config.authorization.enabled cannot be set to true for client agents",
		},

		{
			desc: "auto config authorizer invalid config",
			args: []string{
				`-data-dir=` + dataDir,
				`-server`,
			},
			hcl: []string{`
				auto_config {
					authorization {
						enabled = true
					}
				}
				cert_file = "foo"
			`},
			json: []string{`
			{
				"auto_config": {
					"authorization": {
						"enabled": true
					}
				},
				"cert_file": "foo"
			}`},
			err: `auto_config.authorization.static has invalid configuration: exactly one of 'JWTValidationPubKeys', 'JWKSURL', or 'OIDCDiscoveryURL' must be set for type "jwt"`,
		},

		{
			desc: "auto config authorizer invalid config 2",
			args: []string{
				`-data-dir=` + dataDir,
				`-server`,
			},
			hcl: []string{`
				auto_config {
					authorization {
						enabled = true
						static {
							jwks_url = "https://fake.uri.local"
							oidc_discovery_url = "https://fake.uri.local"
						}
					}
				}
				cert_file = "foo"
			`},
			json: []string{`
			{
				"auto_config": {
					"authorization": {
						"enabled": true,
						"static": {
							"jwks_url": "https://fake.uri.local",
							"oidc_discovery_url": "https://fake.uri.local"
						}
					}
				},
				"cert_file": "foo"
			}`},
			err: `auto_config.authorization.static has invalid configuration: exactly one of 'JWTValidationPubKeys', 'JWKSURL', or 'OIDCDiscoveryURL' must be set for type "jwt"`,
		},

		{
			desc: "auto config authorizer require token replication in secondary",
			args: []string{
				`-data-dir=` + dataDir,
				`-server`,
			},
			hcl: []string{`
				primary_datacenter = "otherdc"
				acl {
					enabled = true
				}
				auto_config {
					authorization {
						enabled = true
						static {
							jwks_url = "https://fake.uri.local"
							oidc_discovery_url = "https://fake.uri.local"
						}
					}
				}
				cert_file = "foo"
			`},
			json: []string{`
			{
				"primary_datacenter": "otherdc",
				"acl": {
					"enabled": true
				},
				"auto_config": {
					"authorization": {
						"enabled": true,
						"static": {
							"jwks_url": "https://fake.uri.local",
							"oidc_discovery_url": "https://fake.uri.local"
						}
					}
				},
				"cert_file": "foo"
			}`},
			err: `Enabling auto-config authorization (auto_config.authorization.enabled) in non primary datacenters with ACLs enabled (acl.enabled) requires also enabling ACL token replication (acl.enable_token_replication)`,
		},

		{
			desc: "auto config authorizer invalid claim assertion",
			args: []string{
				`-data-dir=` + dataDir,
				`-server`,
			},
			hcl: []string{`
				auto_config {
					authorization {
						enabled = true
						static {
							jwt_validation_pub_keys = ["-----BEGIN PUBLIC KEY-----\nMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAERVchfCZng4mmdvQz1+sJHRN40snC\nYt8NjYOnbnScEXMkyoUmASr88gb7jaVAVt3RYASAbgBjB2Z+EUizWkx5Tg==\n-----END PUBLIC KEY-----"]
							claim_assertions = [
								"values.node == ${node}"
							]
						}
					}
				}
				cert_file = "foo"
			`},
			json: []string{`
			{
				"auto_config": {
					"authorization": {
						"enabled": true,
						"static": {
							"jwt_validation_pub_keys": ["-----BEGIN PUBLIC KEY-----\nMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAERVchfCZng4mmdvQz1+sJHRN40snC\nYt8NjYOnbnScEXMkyoUmASr88gb7jaVAVt3RYASAbgBjB2Z+EUizWkx5Tg==\n-----END PUBLIC KEY-----"],
							"claim_assertions": [
								"values.node == ${node}"
							]
						}
					}
				},
				"cert_file": "foo"
			}`},
			err: `auto_config.authorization.static.claim_assertion "values.node == ${node}" is invalid: Selector "values" is not valid`,
		},
		{
			desc: "auto config authorizer ok",
			args: []string{
				`-data-dir=` + dataDir,
				`-server`,
			},
			hcl: []string{`
				auto_config {
					authorization {
						enabled = true
						static {
							jwt_validation_pub_keys = ["-----BEGIN PUBLIC KEY-----\nMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAERVchfCZng4mmdvQz1+sJHRN40snC\nYt8NjYOnbnScEXMkyoUmASr88gb7jaVAVt3RYASAbgBjB2Z+EUizWkx5Tg==\n-----END PUBLIC KEY-----"]
							claim_assertions = [
								"value.node == ${node}"
							]
							claim_mappings = {
								node = "node"
							}
						}
					}
				}
				cert_file = "foo"
			`},
			json: []string{`
			{
				"auto_config": {
					"authorization": {
						"enabled": true,
						"static": {
							"jwt_validation_pub_keys": ["-----BEGIN PUBLIC KEY-----\nMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAERVchfCZng4mmdvQz1+sJHRN40snC\nYt8NjYOnbnScEXMkyoUmASr88gb7jaVAVt3RYASAbgBjB2Z+EUizWkx5Tg==\n-----END PUBLIC KEY-----"],
							"claim_assertions": [
								"value.node == ${node}"
							],
							"claim_mappings": {
								"node": "node"
							}
						}
					}
				},
				"cert_file": "foo"
			}`},
			patch: func(rt *RuntimeConfig) {
				rt.AutoConfig.Authorizer.Enabled = true
				rt.AutoConfig.Authorizer.AuthMethod.Config["ClaimMappings"] = map[string]string{
					"node": "node",
				}
				rt.AutoConfig.Authorizer.AuthMethod.Config["JWTValidationPubKeys"] = []string{"-----BEGIN PUBLIC KEY-----\nMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAERVchfCZng4mmdvQz1+sJHRN40snC\nYt8NjYOnbnScEXMkyoUmASr88gb7jaVAVt3RYASAbgBjB2Z+EUizWkx5Tg==\n-----END PUBLIC KEY-----"}
				rt.AutoConfig.Authorizer.ClaimAssertions = []string{"value.node == ${node}"}
				rt.DataDir = dataDir
				rt.LeaveOnTerm = false
				rt.ServerMode = true
				rt.SkipLeaveOnInt = true
				rt.CertFile = "foo"
			},
		},
		// UI Config tests
		{
			desc: "ui config deprecated",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
				"ui": true,
				"ui_content_path": "/bar"
			}`},
			hcl: []string{`
			ui = true
			ui_content_path = "/bar"
			`},
			warns: []string{
				`The 'ui' field is deprecated. Use the 'ui_config.enabled' field instead.`,
				`The 'ui_content_path' field is deprecated. Use the 'ui_config.content_path' field instead.`,
			},
			patch: func(rt *RuntimeConfig) {
				// Should still work!
				rt.UIConfig.Enabled = true
				rt.UIConfig.ContentPath = "/bar/"
				rt.DataDir = dataDir
			},
		},
		{
			desc: "ui-dir config deprecated",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
				"ui_dir": "/bar"
			}`},
			hcl: []string{`
			ui_dir = "/bar"
			`},
			warns: []string{
				`The 'ui_dir' field is deprecated. Use the 'ui_config.dir' field instead.`,
			},
			patch: func(rt *RuntimeConfig) {
				// Should still work!
				rt.UIConfig.Dir = "/bar"
				rt.DataDir = dataDir
			},
		},
		{
			desc: "metrics_provider constraint",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
				"ui_config": {
					"metrics_provider": "((((lisp 4 life))))"
				}
			}`},
			hcl: []string{`
			ui_config {
				metrics_provider = "((((lisp 4 life))))"
			}
			`},
			err: `ui_config.metrics_provider can only contain lowercase alphanumeric, - or _ characters.`,
		},
		{
			desc: "metrics_provider_options_json invalid JSON",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
				"ui_config": {
					"metrics_provider_options_json": "not valid JSON"
				}
			}`},
			hcl: []string{`
			ui_config {
				metrics_provider_options_json = "not valid JSON"
			}
			`},
			err: `ui_config.metrics_provider_options_json must be empty or a string containing a valid JSON object.`,
		},
		{
			desc: "metrics_provider_options_json not an object",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
				"ui_config": {
					"metrics_provider_options_json": "1.0"
				}
			}`},
			hcl: []string{`
			ui_config {
				metrics_provider_options_json = "1.0"
			}
			`},
			err: `ui_config.metrics_provider_options_json must be empty or a string containing a valid JSON object.`,
		},
		{
			desc: "metrics_proxy.base_url valid",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
				"ui_config": {
					"metrics_proxy": {
						"base_url": "___"
					}
				}
			}`},
			hcl: []string{`
			ui_config {
				metrics_proxy {
					base_url = "___"
				}
			}
			`},
			err: `ui_config.metrics_proxy.base_url must be a valid http or https URL.`,
		},
		{
			desc: "metrics_proxy.path_allowlist invalid (empty)",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
				"ui_config": {
					"metrics_proxy": {
						"path_allowlist": ["", "/foo"]
					}
				}
			}`},
			hcl: []string{`
			ui_config {
				metrics_proxy {
					path_allowlist = ["", "/foo"]
				}
			}
			`},
			err: `ui_config.metrics_proxy.path_allowlist: path "" is not an absolute path`,
		},
		{
			desc: "metrics_proxy.path_allowlist invalid (relative)",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
				"ui_config": {
					"metrics_proxy": {
						"path_allowlist": ["bar/baz", "/foo"]
					}
				}
			}`},
			hcl: []string{`
			ui_config {
				metrics_proxy {
					path_allowlist = ["bar/baz", "/foo"]
				}
			}
			`},
			err: `ui_config.metrics_proxy.path_allowlist: path "bar/baz" is not an absolute path`,
		},
		{
			desc: "metrics_proxy.path_allowlist invalid (weird)",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
				"ui_config": {
					"metrics_proxy": {
						"path_allowlist": ["://bar/baz", "/foo"]
					}
				}
			}`},
			hcl: []string{`
			ui_config {
				metrics_proxy {
					path_allowlist = ["://bar/baz", "/foo"]
				}
			}
			`},
			err: `ui_config.metrics_proxy.path_allowlist: path "://bar/baz" is not an absolute path`,
		},
		{
			desc: "metrics_proxy.path_allowlist invalid (fragment)",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
				"ui_config": {
					"metrics_proxy": {
						"path_allowlist": ["/bar/baz#stuff", "/foo"]
					}
				}
			}`},
			hcl: []string{`
			ui_config {
				metrics_proxy {
					path_allowlist = ["/bar/baz#stuff", "/foo"]
				}
			}
			`},
			err: `ui_config.metrics_proxy.path_allowlist: path "/bar/baz#stuff" is not an absolute path`,
		},
		{
			desc: "metrics_proxy.path_allowlist invalid (querystring)",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
				"ui_config": {
					"metrics_proxy": {
						"path_allowlist": ["/bar/baz?stu=ff", "/foo"]
					}
				}
			}`},
			hcl: []string{`
			ui_config {
				metrics_proxy {
					path_allowlist = ["/bar/baz?stu=ff", "/foo"]
				}
			}
			`},
			err: `ui_config.metrics_proxy.path_allowlist: path "/bar/baz?stu=ff" is not an absolute path`,
		},
		{
			desc: "metrics_proxy.path_allowlist invalid (encoded slash)",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
				"ui_config": {
					"metrics_proxy": {
						"path_allowlist": ["/bar%2fbaz", "/foo"]
					}
				}
			}`},
			hcl: []string{`
			ui_config {
				metrics_proxy {
					path_allowlist = ["/bar%2fbaz", "/foo"]
				}
			}
			`},
			err: `ui_config.metrics_proxy.path_allowlist: path "/bar%2fbaz" is not an absolute path`,
		},
		{
			desc: "metrics_proxy.path_allowlist ok",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
				"ui_config": {
					"metrics_proxy": {
						"path_allowlist": ["/bar/baz", "/foo"]
					}
				}
			}`},
			hcl: []string{`
			ui_config {
				metrics_proxy {
					path_allowlist = ["/bar/baz", "/foo"]
				}
			}
			`},
			patch: func(rt *RuntimeConfig) {
				rt.UIConfig.MetricsProxy.PathAllowlist = []string{"/bar/baz", "/foo"}
				rt.DataDir = dataDir
			},
		},
		{
			desc: "metrics_proxy.path_allowlist defaulted for prometheus",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
				"ui_config": {
					"metrics_provider": "prometheus"
				}
			}`},
			hcl: []string{`
			ui_config {
				metrics_provider = "prometheus"
			}
			`},
			patch: func(rt *RuntimeConfig) {
				rt.UIConfig.MetricsProvider = "prometheus"
				rt.UIConfig.MetricsProxy.PathAllowlist = []string{
					"/api/v1/query",
					"/api/v1/query_range",
				}
				rt.DataDir = dataDir
			},
		},
		{
			desc: "metrics_proxy.path_allowlist not overridden with defaults for prometheus",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
				"ui_config": {
					"metrics_provider": "prometheus",
					"metrics_proxy": {
						"path_allowlist": ["/bar/baz", "/foo"]
					}
				}
			}`},
			hcl: []string{`
			ui_config {
				metrics_provider = "prometheus"
				metrics_proxy {
					path_allowlist = ["/bar/baz", "/foo"]
				}
			}
			`},
			patch: func(rt *RuntimeConfig) {
				rt.UIConfig.MetricsProvider = "prometheus"
				rt.UIConfig.MetricsProxy.PathAllowlist = []string{"/bar/baz", "/foo"}
				rt.DataDir = dataDir
			},
		},
		{
			desc: "metrics_proxy.base_url http(s)",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
				"ui_config": {
					"metrics_proxy": {
						"base_url": "localhost:1234"
					}
				}
			}`},
			hcl: []string{`
			ui_config {
				metrics_proxy {
					base_url = "localhost:1234"
				}
			}
			`},
			err: `ui_config.metrics_proxy.base_url must be a valid http or https URL.`,
		},
		{
			desc: "dashboard_url_templates key format",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
				"ui_config": {
					"dashboard_url_templates": {
						"(*&ASDOUISD)": "localhost:1234"
					}
				}
			}`},
			hcl: []string{`
			ui_config {
				dashboard_url_templates {
					"(*&ASDOUISD)" = "localhost:1234"
				}
			}
			`},
			err: `ui_config.dashboard_url_templates key names can only contain lowercase alphanumeric, - or _ characters.`,
		},
		{
			desc: "dashboard_url_templates value format",
			args: []string{`-data-dir=` + dataDir},
			json: []string{`{
				"ui_config": {
					"dashboard_url_templates": {
						"services": "localhost:1234"
					}
				}
			}`},
			hcl: []string{`
			ui_config {
				dashboard_url_templates {
					services = "localhost:1234"
				}
			}
			`},
			err: `ui_config.dashboard_url_templates values must be a valid http or https URL.`,
		},

		// Per node reconnect timeout test
		{
			desc: "server and advertised reconnect timeout error",
			args: []string{
				`-data-dir=` + dataDir,
				`-server`,
			},
			hcl: []string{`
				advertise_reconnect_timeout = "5s"
			`},
			json: []string{`
			{
				"advertise_reconnect_timeout": "5s"
			}`},
			err: "advertise_reconnect_timeout can only be used on a client",
		},
	}

	testConfig(t, tests, dataDir)
}

func testConfig(t *testing.T, tests []configTest, dataDir string) {
	for _, tt := range tests {
		for pass, format := range []string{"json", "hcl"} {
			// clean data dir before every test
			cleanDir(dataDir)

			// when we test only flags then there are no JSON or HCL
			// sources and we need to make only one pass over the
			// tests.
			flagsOnly := len(tt.json) == 0 && len(tt.hcl) == 0
			if flagsOnly && pass > 0 {
				continue
			}

			// json and hcl sources need to be in sync
			// to make sure we're generating the same config
			if len(tt.json) != len(tt.hcl) {
				t.Fatal(tt.desc, ": JSON and HCL test case out of sync")
			}

			srcs, tails := tt.json, tt.jsontail
			if format == "hcl" {
				srcs, tails = tt.hcl, tt.hcltail
			}

			// build the description
			var desc []string
			if !flagsOnly {
				desc = append(desc, format)
			}
			if tt.desc != "" {
				desc = append(desc, tt.desc)
			}

			t.Run(strings.Join(desc, ":"), func(t *testing.T) {
				flags := BuilderOpts{}

				fs := flag.NewFlagSet("", flag.ContinueOnError)
				AddFlags(fs, &flags)
				err := fs.Parse(tt.args)
				if err != nil {
					t.Fatalf("ParseFlags failed: %s", err)
				}
				require.Len(t, fs.Args(), 0)

				if tt.pre != nil {
					tt.pre()
				}

				// Then create a builder with the flags.
				b, err := NewBuilder(flags)
				require.NoError(t, err)

				patchBuilderShims(b)
				if tt.hostname != nil {
					b.opts.hostname = tt.hostname
				}
				if tt.privatev4 != nil {
					b.opts.getPrivateIPv4 = tt.privatev4
				}
				if tt.publicv6 != nil {
					b.opts.getPublicIPv6 = tt.publicv6
				}

				// read the source fragments
				for i, data := range srcs {
					b.Sources = append(b.Sources, FileSource{
						Name:   fmt.Sprintf("src-%d.%s", i, format),
						Format: format,
						Data:   data,
					})
				}
				for i, data := range tails {
					b.Tail = append(b.Tail, FileSource{
						Name:   fmt.Sprintf("tail-%d.%s", i, format),
						Format: format,
						Data:   data,
					})
				}

				actual, err := b.BuildAndValidate()
				if err == nil && tt.err != "" {
					t.Fatalf("got no error want %q", tt.err)
				}
				if err != nil && tt.err == "" {
					t.Fatalf("got error %s want nil", err)
				}
				if err == nil && tt.err != "" {
					t.Fatalf("got nil want error to contain %q", tt.err)
				}
				if err != nil && tt.err != "" && !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.err)
				}
				if tt.err != "" {
					return
				}
				require.Equal(t, tt.warns, b.Warnings, "warnings")

				// build a default configuration, then patch the fields we expect to change
				// and compare it with the generated configuration. Since the expected
				// runtime config has been validated we do not need to validate it again.
				x, err := NewBuilder(BuilderOpts{})
				if err != nil {
					t.Fatal(err)
				}
				patchBuilderShims(x)
				expected, err := x.Build()
				require.NoError(t, err)
				if tt.patch != nil {
					tt.patch(&expected)
				}

				// both DataDir fields should always be the same, so test for the
				// invariant, and than updated the expected, so that every test
				// case does not need to set this field.
				require.Equal(t, actual.DataDir, actual.ACLTokens.DataDir)
				expected.ACLTokens.DataDir = actual.ACLTokens.DataDir

				if tt.patchActual != nil {
					tt.patchActual(&actual)
				}
				assertDeepEqual(t, expected, actual, cmpopts.EquateEmpty())
			})
		}
	}
}

func assertDeepEqual(t *testing.T, x, y interface{}, opts ...cmp.Option) {
	t.Helper()
	if diff := cmp.Diff(x, y, opts...); diff != "" {
		t.Fatalf("assertion failed: values are not equal\n--- expected\n+++ actual\n%v", diff)
	}
}

func TestNewBuilder_InvalidConfigFormat(t *testing.T) {
	_, err := NewBuilder(BuilderOpts{ConfigFormat: "yaml"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "-config-format must be either 'hcl' or 'json'")
}

// TestFullConfig tests the conversion from a fully populated JSON or
// HCL config file to a RuntimeConfig structure. All fields must be set
// to a unique non-zero value.
//
// To aid populating the fields the following bash functions can be used
// to generate random strings and ints:
//
//   random-int() { echo $RANDOM }
//   random-string() { base64 /dev/urandom | tr -d '/+' | fold -w ${1:-32} | head -n 1 }
//
// To generate a random string of length 8 run the following command in
// a terminal:
//
//   random-string 8
//
func TestFullConfig(t *testing.T) {
	dataDir := testutil.TempDir(t, "consul")

	cidr := func(s string) *net.IPNet {
		_, n, _ := net.ParseCIDR(s)
		return n
	}

	defaultEntMeta := structs.DefaultEnterpriseMeta()

	flagSrc := []string{`-dev`}
	src := map[string]string{
		"json": `{
			"acl_agent_master_token": "furuQD0b",
			"acl_agent_token": "cOshLOQ2",
			"acl_datacenter": "m3urck3z",
			"acl_default_policy": "ArK3WIfE",
			"acl_down_policy": "vZXMfMP0",
			"acl_enable_key_list_policy": true,
			"acl_master_token": "C1Q1oIwh",
			"acl_replication_token": "LMmgy5dO",
			"acl_token": "O1El0wan",
			"acl_ttl": "18060s",
			"acl" : {
				"enabled" : true,
				"down_policy" : "03eb2aee",
				"default_policy" : "72c2e7a0",
				"enable_key_list_policy": true,
				"enable_token_persistence": true,
				"policy_ttl": "1123s",
				"role_ttl": "9876s",
				"token_ttl": "3321s",
				"enable_token_replication" : true,
				"msp_disable_bootstrap": true,
				"tokens" : {
					"master" : "8a19ac27",
					"agent_master" : "64fd0e08",
					"replication" : "5795983a",
					"agent" : "bed2377c",
					"default" : "418fdff1",
					"managed_service_provider": [
						{
							"accessor_id": "first", 
							"secret_id": "fb0cee1f-2847-467c-99db-a897cff5fd4d"
						}, 
						{
							"accessor_id": "second", 
							"secret_id": "1046c8da-e166-4667-897a-aefb343db9db"
						}
					]
				}
			},
			"addresses": {
				"dns": "93.95.95.81",
				"http": "83.39.91.39",
				"https": "95.17.17.19",
				"grpc": "32.31.61.91"
			},
			"advertise_addr": "17.99.29.16",
			"advertise_addr_wan": "78.63.37.19",
			"advertise_reconnect_timeout": "0s",
			"audit": {
				"enabled": false
			},
			"auto_config": {
				"enabled": false,
				"intro_token": "OpBPGRwt",
				"intro_token_file": "gFvAXwI8",
				"dns_sans": ["6zdaWg9J"],
				"ip_sans": ["198.18.99.99"],
				"server_addresses": ["198.18.100.1"],
				"authorization": {
					"enabled": true,
					"static": {
						"allow_reuse": true,
						"claim_mappings": {
							"node": "node"
						},
						"list_claim_mappings": {
							"foo": "bar"
						},
						"bound_issuer": "consul",
						"bound_audiences": ["consul-cluster-1"],
						"claim_assertions": ["value.node == \"${node}\""],
						"jwt_validation_pub_keys": ["-----BEGIN PUBLIC KEY-----\nMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAERVchfCZng4mmdvQz1+sJHRN40snC\nYt8NjYOnbnScEXMkyoUmASr88gb7jaVAVt3RYASAbgBjB2Z+EUizWkx5Tg==\n-----END PUBLIC KEY-----"]
					}
				}
			},
			"autopilot": {
				"cleanup_dead_servers": true,
				"disable_upgrade_migration": true,
				"last_contact_threshold": "12705s",
				"max_trailing_logs": 17849,
				"min_quorum":		 3,
				"redundancy_zone_tag": "3IsufDJf",
				"server_stabilization_time": "23057s",
				"upgrade_version_tag": "W9pDwFAL"
			},
			"bind_addr": "16.99.34.17",
			"bootstrap": true,
			"bootstrap_expect": 53,
			"cache": {
				"entry_fetch_max_burst": 42,
				"entry_fetch_rate": 0.334
			},
			"use_streaming_backend": true,
			"ca_file": "erA7T0PM",
			"ca_path": "mQEN1Mfp",
			"cert_file": "7s4QAzDk",
			"check": {
				"id": "fZaCAXww",
				"name": "OOM2eo0f",
				"notes": "zXzXI9Gt",
				"service_id": "L8G0QNmR",
				"token": "oo4BCTgJ",
				"status": "qLykAl5u",
				"args": ["f3BemRjy", "e5zgpef7"],
				"http": "29B93haH",
				"header": {
					"hBq0zn1q": [ "2a9o9ZKP", "vKwA5lR6" ],
					"f3r6xFtM": [ "RyuIdDWv", "QbxEcIUM" ]
				},
				"method": "Dou0nGT5",
				"body": "5PBQd2OT",
				"output_max_size": ` + strconv.Itoa(checks.DefaultBufSize) + `,
				"tcp": "JY6fTTcw",
				"interval": "18714s",
				"docker_container_id": "qF66POS9",
				"shell": "sOnDy228",
				"tls_skip_verify": true,
				"timeout": "5954s",
				"ttl": "30044s",
				"deregister_critical_service_after": "13209s"
			},
			"checks": [
				{
					"id": "uAjE6m9Z",
					"name": "QsZRGpYr",
					"notes": "VJ7Sk4BY",
					"service_id": "lSulPcyz",
					"token": "toO59sh8",
					"status": "9RlWsXMV",
					"args": ["4BAJttck", "4D2NPtTQ"],
					"http": "dohLcyQ2",
					"header": {
						"ZBfTin3L": [ "1sDbEqYG", "lJGASsWK" ],
						"Ui0nU99X": [ "LMccm3Qe", "k5H5RggQ" ]
					},
					"method": "aldrIQ4l",
					"body": "wSjTy7dg",
					"tcp": "RJQND605",
					"interval": "22164s",
					"output_max_size": ` + strconv.Itoa(checks.DefaultBufSize) + `,
					"docker_container_id": "ipgdFtjd",
					"shell": "qAeOYy0M",
					"tls_skip_verify": true,
					"timeout": "1813s",
					"ttl": "21743s",
					"deregister_critical_service_after": "14232s"
				},
				{
					"id": "Cqq95BhP",
					"name": "3qXpkS0i",
					"notes": "sb5qLTex",
					"service_id": "CmUUcRna",
					"token": "a3nQzHuy",
					"status": "irj26nf3",
					"args": ["9s526ogY", "gSlOHj1w"],
					"http": "yzhgsQ7Y",
					"header": {
						"zcqwA8dO": [ "qb1zx0DL", "sXCxPFsD" ],
						"qxvdnSE9": [ "6wBPUYdF", "YYh8wtSZ" ]
					},
					"method": "gLrztrNw",
					"body": "0jkKgGUC",
					"tcp": "4jG5casb",
					"interval": "28767s",
					"output_max_size": ` + strconv.Itoa(checks.DefaultBufSize) + `,
					"docker_container_id": "THW6u7rL",
					"shell": "C1Zt3Zwh",
					"tls_skip_verify": true,
					"timeout": "18506s",
					"ttl": "31006s",
					"deregister_critical_service_after": "2366s"
				}
			],
			"check_update_interval": "16507s",
			"client_addr": "93.83.18.19",
			"config_entries": {
				"bootstrap": [
					{
						"kind": "proxy-defaults",
						"name": "global",
						"config": {
							"foo": "bar",
							"bar": 1.0
						}
					}
				]
                        },
			"auto_encrypt": {
				"tls": false,
				"dns_san": ["a.com", "b.com"],
				"ip_san": ["192.168.4.139", "192.168.4.140"],
				"allow_tls": true
			},
			"connect": {
				"ca_provider": "consul",
				"ca_config": {
					"rotation_period": "90h",
					"intermediate_cert_ttl": "8760h",
					"leaf_cert_ttl": "1h",
					"csr_max_per_second": 100,
					"csr_max_concurrent": 2
				},
				"enable_mesh_gateway_wan_federation": false,
				"enabled": true
			},
			"gossip_lan" : {
				"gossip_nodes": 6,
				"gossip_interval" : "25252s",
				"retransmit_mult" : 1234,
				"suspicion_mult"  : 1235,
				"probe_interval"  : "101ms",
				"probe_timeout"   : "102ms"
			},
			"gossip_wan" : {
				"gossip_nodes" : 2,
				"gossip_interval" : "6966s",
				"retransmit_mult" : 16384,
				"suspicion_mult"  : 16385,
				"probe_interval" : "103ms",
				"probe_timeout"  : "104ms"
			},
			"data_dir": "` + dataDir + `",
			"datacenter": "rzo029wg",
			"default_query_time": "16743s",
			"disable_anonymous_signature": true,
			"disable_coordinates": true,
			"disable_host_node_id": true,
			"disable_http_unprintable_char_filter": true,
			"disable_keyring_file": true,
			"disable_remote_exec": true,
			"disable_update_check": true,
			"discard_check_output": true,
			"discovery_max_stale": "5s",
			"domain": "7W1xXSqd",
			"alt_domain": "1789hsd",
			"dns_config": {
				"allow_stale": true,
				"a_record_limit": 29907,
				"disable_compression": true,
				"enable_truncate": true,
				"max_stale": "29685s",
				"node_ttl": "7084s",
				"only_passing": true,
				"recursor_timeout": "4427s",
				"service_ttl": {
					"*": "32030s"
				},
				"udp_answer_limit": 29909,
				"use_cache": true,
				"cache_max_age": "5m",
				"prefer_namespace": true
			},
			"enable_acl_replication": true,
			"enable_agent_tls_for_checks": true,
			"enable_central_service_config": false,
			"enable_debug": true,
			"enable_script_checks": true,
			"enable_local_script_checks": true,
			"enable_syslog": true,
			"encrypt": "A4wELWqH",
			"encrypt_verify_incoming": true,
			"encrypt_verify_outgoing": true,
			"http_config": {
				"block_endpoints": [ "RBvAFcGD", "fWOWFznh" ],
				"allow_write_http_from": [ "127.0.0.1/8", "22.33.44.55/32", "0.0.0.0/0" ],
				"response_headers": {
					"M6TKa9NP": "xjuxjOzQ",
					"JRCrHZed": "rl0mTx81"
				},
				"use_cache": false,
				"max_header_bytes": 10
			},
			"key_file": "IEkkwgIA",
			"leave_on_terminate": true,
			"limits": {
				"http_max_conns_per_client": 100,
				"https_handshake_timeout": "2391ms",
				"rpc_handshake_timeout": "1932ms",
				"rpc_rate": 12029.43,
				"rpc_max_burst": 44848,
				"rpc_max_conns_per_client": 2954,
				"kv_max_value_size": 1234567800000000,
				"txn_max_req_len": 5678000000000000
			},
			"log_level": "k1zo9Spt",
			"log_json": true,
			"max_query_time": "18237s",
			"node_id": "AsUIlw99",
			"node_meta": {
				"5mgGQMBk": "mJLtVMSG",
				"A7ynFMJB": "0Nx6RGab"
			},
			"node_name": "otlLxGaI",
			"non_voting_server": true,
			"performance": {
				"leave_drain_time": "8265s",
				"raft_multiplier": 5,
				"rpc_hold_timeout": "15707s"
			},
			"pid_file": "43xN80Km",
			"ports": {
				"dns": 7001,
				"http": 7999,
				"https": 15127,
				"server": 3757,
				"grpc": 4881,
				"sidecar_min_port": 8888,
				"sidecar_max_port": 9999,
				"expose_min_port": 1111,
				"expose_max_port": 2222
			},
			"protocol": 30793,
			"primary_datacenter": "ejtmd43d",
			"primary_gateways": [ "aej8eeZo", "roh2KahS" ],
			"primary_gateways_interval": "18866s",
			"raft_protocol": 3,
			"raft_snapshot_threshold": 16384,
			"raft_snapshot_interval": "30s",
			"raft_trailing_logs": 83749,
			"read_replica": true,
			"reconnect_timeout": "23739s",
			"reconnect_timeout_wan": "26694s",
			"recursors": [ "63.38.39.58", "92.49.18.18" ],
			"rejoin_after_leave": true,
			"retry_interval": "8067s",
			"retry_interval_wan": "28866s",
			"retry_join": [ "pbsSFY7U", "l0qLtWij" ],
			"retry_join_wan": [ "PFsR02Ye", "rJdQIhER" ],
			"retry_max": 913,
			"retry_max_wan": 23160,
			"rpc": {"enable_streaming": true},
			"segment": "BC2NhTDi",
			"segments": [
				{
					"name": "PExYMe2E",
					"bind": "36.73.36.19",
					"port": 38295,
					"rpc_listener": true,
					"advertise": "63.39.19.18"
				},
				{
					"name": "UzCvJgup",
					"bind": "37.58.38.19",
					"port": 39292,
					"rpc_listener": true,
					"advertise": "83.58.26.27"
				}
			],
			"serf_lan": "99.43.63.15",
			"serf_wan": "67.88.33.19",
			"server": true,
			"server_name": "Oerr9n1G",
			"service": {
				"id": "dLOXpSCI",
				"name": "o1ynPkp0",
				"meta": {
					"mymeta": "data"
				},
				"tagged_addresses": {
					"lan": {
						"address": "2d79888a",
						"port": 2143
					},
					"wan": {
						"address": "d4db85e2",
						"port": 6109
					}
				},
				"tags": ["nkwshvM5", "NTDWn3ek"],
				"address": "cOlSOhbp",
				"token": "msy7iWER",
				"port": 24237,
				"weights": {
					"passing": 100,
					"warning": 1
				},
				"enable_tag_override": true,
				"check": {
					"id": "RMi85Dv8",
					"name": "iehanzuq",
					"status": "rCvn53TH",
					"notes": "fti5lfF3",
					"args": ["16WRUmwS", "QWk7j7ae"],
					"http": "dl3Fgme3",
					"header": {
						"rjm4DEd3": ["2m3m2Fls"],
						"l4HwQ112": ["fk56MNlo", "dhLK56aZ"]
					},
					"method": "9afLm3Mj",
					"body": "wVVL2V6f",
					"tcp": "fjiLFqVd",
					"interval": "23926s",
					"output_max_size": ` + strconv.Itoa(checks.DefaultBufSize) + `,
					"docker_container_id": "dO5TtRHk",
					"shell": "e6q2ttES",
					"tls_skip_verify": true,
					"timeout": "38483s",
					"ttl": "10943s",
					"deregister_critical_service_after": "68787s"
				},
				"checks": [
					{
						"id": "Zv99e9Ka",
						"name": "sgV4F7Pk",
						"notes": "yP5nKbW0",
						"status": "7oLMEyfu",
						"args": ["5wEZtZpv", "0Ihyk8cS"],
						"http": "KyDjGY9H",
						"header": {
							"gv5qefTz": [ "5Olo2pMG", "PvvKWQU5" ],
							"SHOVq1Vv": [ "jntFhyym", "GYJh32pp" ]
						},
						"method": "T66MFBfR",
						"body": "OwGjTFQi",
						"tcp": "bNnNfx2A",
						"interval": "22224s",
						"output_max_size": ` + strconv.Itoa(checks.DefaultBufSize) + `,
						"docker_container_id": "ipgdFtjd",
						"shell": "omVZq7Sz",
						"tls_skip_verify": true,
						"timeout": "18913s",
						"ttl": "44743s",
						"deregister_critical_service_after": "8482s"
					},
					{
						"id": "G79O6Mpr",
						"name": "IEqrzrsd",
						"notes": "SVqApqeM",
						"status": "XXkVoZXt",
						"args": ["wD05Bvao", "rLYB7kQC"],
						"http": "kyICZsn8",
						"header": {
							"4ebP5vL4": [ "G20SrL5Q", "DwPKlMbo" ],
							"p2UI34Qz": [ "UsG1D0Qh", "NHhRiB6s" ]
						},
						"method": "ciYHWors",
						"body": "lUVLGYU7",
						"tcp": "FfvCwlqH",
						"interval": "12356s",
						"output_max_size": ` + strconv.Itoa(checks.DefaultBufSize) + `,
						"docker_container_id": "HBndBU6R",
						"shell": "hVI33JjA",
						"tls_skip_verify": true,
						"timeout": "38282s",
						"ttl": "1181s",
						"deregister_critical_service_after": "4992s"
					}
				],
				"connect": {
					"native": true
				}
			},
			"services": [
				{
					"id": "wI1dzxS4",
					"name": "7IszXMQ1",
					"tags": ["0Zwg8l6v", "zebELdN5"],
					"address": "9RhqPSPB",
					"token": "myjKJkWH",
					"port": 72219,
					"enable_tag_override": true,
					"check": {
						"id": "qmfeO5if",
						"name": "atDGP7n5",
						"status": "pDQKEhWL",
						"notes": "Yt8EDLev",
						"args": ["81EDZLPa", "bPY5X8xd"],
						"http": "qzHYvmJO",
						"header": {
							"UkpmZ3a3": ["2dfzXuxZ"],
							"cVFpko4u": ["gGqdEB6k", "9LsRo22u"]
						},
						"method": "X5DrovFc",
						"body": "WeikigLh",
						"tcp": "ICbxkpSF",
						"interval": "24392s",
						"output_max_size": ` + strconv.Itoa(checks.DefaultBufSize) + `,
						"docker_container_id": "ZKXr68Yb",
						"shell": "CEfzx0Fo",
						"tls_skip_verify": true,
						"timeout": "38333s",
						"ttl": "57201s",
						"deregister_critical_service_after": "44214s"
					},
					"connect": {
						"sidecar_service": {}
					}
				},
				{
					"id": "MRHVMZuD",
					"name": "6L6BVfgH",
					"tags": ["7Ale4y6o", "PMBW08hy"],
					"address": "R6H6g8h0",
					"token": "ZgY8gjMI",
					"port": 38292,
					"weights": {
						"passing": 1979,
						"warning": 6
					},
					"enable_tag_override": true,
					"checks": [
						{
							"id": "GTti9hCo",
							"name": "9OOS93ne",
							"notes": "CQy86DH0",
							"status": "P0SWDvrk",
							"args": ["EXvkYIuG", "BATOyt6h"],
							"http": "u97ByEiW",
							"header": {
								"MUlReo8L": [ "AUZG7wHG", "gsN0Dc2N" ],
								"1UJXjVrT": [ "OJgxzTfk", "xZZrFsq7" ]
							},
							"method": "5wkAxCUE",
							"body": "7CRjCJyz",
							"tcp": "MN3oA9D2",
							"interval": "32718s",
							"output_max_size": ` + strconv.Itoa(checks.DefaultBufSize) + `,
							"docker_container_id": "cU15LMet",
							"shell": "nEz9qz2l",
							"tls_skip_verify": true,
							"timeout": "34738s",
							"ttl": "22773s",
							"deregister_critical_service_after": "84282s"
						},
						{
							"id": "UHsDeLxG",
							"name": "PQSaPWlT",
							"notes": "jKChDOdl",
							"status": "5qFz6OZn",
							"args": ["NMtYWlT9", "vj74JXsm"],
							"http": "1LBDJhw4",
							"header": {
								"cXPmnv1M": [ "imDqfaBx", "NFxZ1bQe" ],
								"vr7wY7CS": [ "EtCoNPPL", "9vAarJ5s" ]
							},
							"method": "wzByP903",
							"body": "4I8ucZgZ",
							"tcp": "2exjZIGE",
							"interval": "5656s",
							"output_max_size": ` + strconv.Itoa(checks.DefaultBufSize) + `,
							"docker_container_id": "5tDBWpfA",
							"shell": "rlTpLM8s",
							"tls_skip_verify": true,
							"timeout": "4868s",
							"ttl": "11222s",
							"deregister_critical_service_after": "68482s"
						}
					],
					"connect": {}
				},
				{
					"id": "Kh81CPF6",
					"kind": "connect-proxy",
					"name": "Kh81CPF6-proxy",
					"port": 31471,
					"proxy": {
						"config": {
								"cedGGtZf": "pWrUNiWw"
						},
						"destination_service_id": "6L6BVfgH-id",
						"destination_service_name": "6L6BVfgH",
						"local_service_address": "127.0.0.2",
						"local_service_port": 23759,
						"expose": {
							"checks": true,
							"paths": [
								{
									"path": "/health",
									"local_path_port": 8080,
									"listener_port": 21500,
									"protocol": "http"
								}
							]
						},
						"upstreams": [
							{
								"destination_name": "KPtAj2cb",
								"local_bind_port": 4051,
								"config": {
									"kzRnZOyd": "nUNKoL8H"
								}
							},
							{
								"destination_name": "KSd8HsRl",
								"destination_namespace": "9nakw0td",
								"destination_type": "prepared_query",
								"local_bind_address": "127.24.88.0",
								"local_bind_port": 11884
							}
						]
					}
				},
				{
					"id": "kvVqbwSE",
					"kind": "mesh-gateway",
					"name": "gw-primary-dc",
					"port": 27147,
					"proxy": {
						"config": {
							"1CuJHVfw" : "Kzqsa7yc"
						}
					}
				}
			],
			"session_ttl_min": "26627s",
			"skip_leave_on_interrupt": true,
			"start_join": [ "LR3hGDoG", "MwVpZ4Up" ],
			"start_join_wan": [ "EbFSc3nA", "kwXTh623" ],
			"syslog_facility": "hHv79Uia",
			"tagged_addresses": {
				"7MYgHrYH": "dALJAhLD",
				"h6DdBy6K": "ebrr9zZ8"
			},
			"telemetry": {
				"circonus_api_app": "p4QOTe9j",
				"circonus_api_token": "E3j35V23",
				"circonus_api_url": "mEMjHpGg",
				"circonus_broker_id": "BHlxUhed",
				"circonus_broker_select_tag": "13xy1gHm",
				"circonus_check_display_name": "DRSlQR6n",
				"circonus_check_force_metric_activation": "Ua5FGVYf",
				"circonus_check_id": "kGorutad",
				"circonus_check_instance_id": "rwoOL6R4",
				"circonus_check_search_tag": "ovT4hT4f",
				"circonus_check_tags": "prvO4uBl",
				"circonus_submission_interval": "DolzaflP",
				"circonus_submission_url": "gTcbS93G",
				"disable_hostname": true,
				"dogstatsd_addr": "0wSndumK",
				"dogstatsd_tags": [ "3N81zSUB","Xtj8AnXZ" ],
				"filter_default": true,
				"prefix_filter": [ "+oJotS8XJ","-cazlEhGn" ],
				"metrics_prefix": "ftO6DySn",
				"prometheus_retention_time": "15s",
				"statsd_address": "drce87cy",
				"statsite_address": "HpFwKB8R",
				"disable_compat_1.9": true
			},
			"tls_cipher_suites": "TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256",
			"tls_min_version": "pAOWafkR",
			"tls_prefer_server_cipher_suites": true,
			"translate_wan_addrs": true,
			"ui_config": {
				"enabled": true,
				"dir": "pVncV4Ey",
				"content_path": "qp1WRhYH",
				"metrics_provider": "sgnaoa_lower_case",
				"metrics_provider_files": ["sgnaMFoa", "dicnwkTH"],
				"metrics_provider_options_json": "{\"DIbVQadX\": 1}",
				"metrics_proxy": {
					"base_url": "http://foo.bar",
					"add_headers": [
						{
							"name": "p3nynwc9",
							"value": "TYBgnN2F"
						}
					],
					"path_allowlist": ["/aSh3cu", "/eiK/2Th"]
				},
				"dashboard_url_templates": {
					"u2eziu2n_lower_case": "http://lkjasd.otr"
				}
			},
			"unix_sockets": {
				"group": "8pFodrV8",
				"mode": "E8sAwOv4",
				"user": "E0nB1DwA"
			},
			"verify_incoming": true,
			"verify_incoming_https": true,
			"verify_incoming_rpc": true,
			"verify_outgoing": true,
			"verify_server_hostname": true,
			"watches": [
				{
					"type": "key",
					"datacenter": "GyE6jpeW",
					"key": "j9lF1Tve",
					"handler": "90N7S4LN"
				}, {
					"type": "keyprefix",
					"datacenter": "fYrl3F5d",
					"key": "sl3Dffu7",
					"args": ["dltjDJ2a", "flEa7C2d"]
				}
			]
		}`,
		"hcl": `
			acl_agent_master_token = "furuQD0b"
			acl_agent_token = "cOshLOQ2"
			acl_datacenter = "m3urck3z"
			acl_default_policy = "ArK3WIfE"
			acl_down_policy = "vZXMfMP0"
			acl_enable_key_list_policy = true
			acl_master_token = "C1Q1oIwh"
			acl_replication_token = "LMmgy5dO"
			acl_token = "O1El0wan"
			acl_ttl = "18060s"
			acl = {
				enabled = true
				down_policy = "03eb2aee"
				default_policy = "72c2e7a0"
				enable_key_list_policy = true
				enable_token_persistence = true
				policy_ttl = "1123s"
				role_ttl = "9876s"
				token_ttl = "3321s"
				enable_token_replication = true
				msp_disable_bootstrap = true
				tokens = {
					master = "8a19ac27",
					agent_master = "64fd0e08",
					replication = "5795983a",
					agent = "bed2377c",
					default = "418fdff1",
					managed_service_provider = [
						{
							accessor_id = "first", 
							secret_id = "fb0cee1f-2847-467c-99db-a897cff5fd4d"
						}, 
						{
							accessor_id = "second", 
							secret_id = "1046c8da-e166-4667-897a-aefb343db9db"
						}
					]
				}
			}
			addresses = {
				dns = "93.95.95.81"
				http = "83.39.91.39"
				https = "95.17.17.19"
				grpc = "32.31.61.91"
			}
			advertise_addr = "17.99.29.16"
			advertise_addr_wan = "78.63.37.19"
			advertise_reconnect_timeout = "0s"
			audit = {
				enabled = false
			}
			auto_config = {
				enabled = false
				intro_token = "OpBPGRwt"
				intro_token_file = "gFvAXwI8"
				dns_sans = ["6zdaWg9J"]
				ip_sans = ["198.18.99.99"]
				server_addresses = ["198.18.100.1"]
				authorization = {
					enabled = true
					static {
						allow_reuse = true
						claim_mappings = {
							node = "node"
						}
						list_claim_mappings = {
							foo = "bar"
						}
						bound_issuer = "consul"
						bound_audiences = ["consul-cluster-1"]
						claim_assertions = ["value.node == \"${node}\""]
						jwt_validation_pub_keys = ["-----BEGIN PUBLIC KEY-----\nMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAERVchfCZng4mmdvQz1+sJHRN40snC\nYt8NjYOnbnScEXMkyoUmASr88gb7jaVAVt3RYASAbgBjB2Z+EUizWkx5Tg==\n-----END PUBLIC KEY-----"]
					}
				}
			}
			autopilot = {
				cleanup_dead_servers = true
				disable_upgrade_migration = true
				last_contact_threshold = "12705s"
				max_trailing_logs = 17849
				min_quorum = 3
				redundancy_zone_tag = "3IsufDJf"
				server_stabilization_time = "23057s"
				upgrade_version_tag = "W9pDwFAL"
			}
			bind_addr = "16.99.34.17"
			bootstrap = true
			bootstrap_expect = 53
			cache = {
				entry_fetch_max_burst = 42
				entry_fetch_rate = 0.334
			},
            use_streaming_backend = true
			ca_file = "erA7T0PM"
			ca_path = "mQEN1Mfp"
			cert_file = "7s4QAzDk"
			check = {
				id = "fZaCAXww"
				name = "OOM2eo0f"
				notes = "zXzXI9Gt"
				service_id = "L8G0QNmR"
				token = "oo4BCTgJ"
				status = "qLykAl5u"
				args = ["f3BemRjy", "e5zgpef7"]
				http = "29B93haH"
				header = {
					hBq0zn1q = [ "2a9o9ZKP", "vKwA5lR6" ]
					f3r6xFtM = [ "RyuIdDWv", "QbxEcIUM" ]
				}
				method = "Dou0nGT5"
				body = "5PBQd2OT"
				tcp = "JY6fTTcw"
				interval = "18714s"
				output_max_size = ` + strconv.Itoa(checks.DefaultBufSize) + `
				docker_container_id = "qF66POS9"
				shell = "sOnDy228"
				tls_skip_verify = true
				timeout = "5954s"
				ttl = "30044s"
				deregister_critical_service_after = "13209s"
			},
			checks = [
				{
					id = "uAjE6m9Z"
					name = "QsZRGpYr"
					notes = "VJ7Sk4BY"
					service_id = "lSulPcyz"
					token = "toO59sh8"
					status = "9RlWsXMV"
					args = ["4BAJttck", "4D2NPtTQ"]
					http = "dohLcyQ2"
					header = {
						"ZBfTin3L" = [ "1sDbEqYG", "lJGASsWK" ]
						"Ui0nU99X" = [ "LMccm3Qe", "k5H5RggQ" ]
					}
					method = "aldrIQ4l"
					body = "wSjTy7dg"
					tcp = "RJQND605"
					interval = "22164s"
					output_max_size = ` + strconv.Itoa(checks.DefaultBufSize) + `
					docker_container_id = "ipgdFtjd"
					shell = "qAeOYy0M"
					tls_skip_verify = true
					timeout = "1813s"
					ttl = "21743s"
					deregister_critical_service_after = "14232s"
				},
				{
					id = "Cqq95BhP"
					name = "3qXpkS0i"
					notes = "sb5qLTex"
					service_id = "CmUUcRna"
					token = "a3nQzHuy"
					status = "irj26nf3"
					args = ["9s526ogY", "gSlOHj1w"]
					http = "yzhgsQ7Y"
					header = {
						"zcqwA8dO" = [ "qb1zx0DL", "sXCxPFsD" ]
						"qxvdnSE9" = [ "6wBPUYdF", "YYh8wtSZ" ]
					}
					method = "gLrztrNw"
					body = "0jkKgGUC"
					tcp = "4jG5casb"
					interval = "28767s"
					output_max_size = ` + strconv.Itoa(checks.DefaultBufSize) + `
					docker_container_id = "THW6u7rL"
					shell = "C1Zt3Zwh"
					tls_skip_verify = true
					timeout = "18506s"
					ttl = "31006s"
					deregister_critical_service_after = "2366s"
				}
			]
			check_update_interval = "16507s"
			client_addr = "93.83.18.19"
			config_entries {
				# This is using the repeated block-to-array HCL magic
				bootstrap {
					kind = "proxy-defaults"
					name = "global"
					config {
						foo = "bar"
						bar = 1.0
					}
				}
			}
			auto_encrypt = {
				tls = false
				dns_san = ["a.com", "b.com"]
				ip_san = ["192.168.4.139", "192.168.4.140"]
				allow_tls = true
			}
			connect {
				ca_provider = "consul"
				ca_config {
					rotation_period = "90h"
					intermediate_cert_ttl = "8760h"
					leaf_cert_ttl = "1h"
					# hack float since json parses numbers as float and we have to
					# assert against the same thing
					csr_max_per_second = 100.0
					csr_max_concurrent = 2.0
				}
				enable_mesh_gateway_wan_federation = false
				enabled = true
			}
			gossip_lan {
				gossip_nodes    = 6
				gossip_interval = "25252s"
				retransmit_mult = 1234
				suspicion_mult  = 1235
				probe_interval  = "101ms"
				probe_timeout   = "102ms"
			}
			gossip_wan {
				gossip_nodes    = 2
				gossip_interval = "6966s"
				retransmit_mult = 16384
				suspicion_mult  = 16385
				probe_interval  = "103ms"
				probe_timeout   = "104ms"
			}
			data_dir = "` + dataDir + `"
			datacenter = "rzo029wg"
			default_query_time = "16743s"
			disable_anonymous_signature = true
			disable_coordinates = true
			disable_host_node_id = true
			disable_http_unprintable_char_filter = true
			disable_keyring_file = true
			disable_remote_exec = true
			disable_update_check = true
			discard_check_output = true
			discovery_max_stale = "5s"
			domain = "7W1xXSqd"
			alt_domain = "1789hsd"
			dns_config {
				allow_stale = true
				a_record_limit = 29907
				disable_compression = true
				enable_truncate = true
				max_stale = "29685s"
				node_ttl = "7084s"
				only_passing = true
				recursor_timeout = "4427s"
				service_ttl = {
					"*" = "32030s"
				}
				udp_answer_limit = 29909
				use_cache = true
				cache_max_age = "5m"
				prefer_namespace = true
			}
			enable_acl_replication = true
			enable_agent_tls_for_checks = true
			enable_central_service_config = false
			enable_debug = true
			enable_script_checks = true
			enable_local_script_checks = true
			enable_syslog = true
			encrypt = "A4wELWqH"
			encrypt_verify_incoming = true
			encrypt_verify_outgoing = true
			http_config {
				block_endpoints = [ "RBvAFcGD", "fWOWFznh" ]
				allow_write_http_from = [ "127.0.0.1/8", "22.33.44.55/32", "0.0.0.0/0" ]
				response_headers = {
					"M6TKa9NP" = "xjuxjOzQ"
					"JRCrHZed" = "rl0mTx81"
				}
				use_cache = false
				max_header_bytes = 10
			}
			key_file = "IEkkwgIA"
			leave_on_terminate = true
			limits {
				http_max_conns_per_client = 100
				https_handshake_timeout = "2391ms"
				rpc_handshake_timeout = "1932ms"
				rpc_rate = 12029.43
				rpc_max_burst = 44848
				rpc_max_conns_per_client = 2954
				kv_max_value_size = 1234567800000000
				txn_max_req_len = 5678000000000000
			}
			log_level = "k1zo9Spt"
			log_json = true
			max_query_time = "18237s"
			node_id = "AsUIlw99"
			node_meta {
				"5mgGQMBk" = "mJLtVMSG"
				"A7ynFMJB" = "0Nx6RGab"
			}
			node_name = "otlLxGaI"
			non_voting_server = true
			performance {
				leave_drain_time = "8265s"
				raft_multiplier = 5
				rpc_hold_timeout = "15707s"
			}
			pid_file = "43xN80Km"
			ports {
				dns = 7001
				http = 7999
				https = 15127
				server = 3757
				grpc = 4881
				proxy_min_port = 2000
				proxy_max_port = 3000
				sidecar_min_port = 8888
				sidecar_max_port = 9999
				expose_min_port = 1111
				expose_max_port = 2222
			}
			protocol = 30793
			primary_datacenter = "ejtmd43d"
			primary_gateways = [ "aej8eeZo", "roh2KahS" ]
			primary_gateways_interval = "18866s"
			raft_protocol = 3
			raft_snapshot_threshold = 16384
			raft_snapshot_interval = "30s"
			raft_trailing_logs = 83749
			read_replica = true
			reconnect_timeout = "23739s"
			reconnect_timeout_wan = "26694s"
			recursors = [ "63.38.39.58", "92.49.18.18" ]
			rejoin_after_leave = true
			retry_interval = "8067s"
			retry_interval_wan = "28866s"
			retry_join = [ "pbsSFY7U", "l0qLtWij" ]
			retry_join_wan = [ "PFsR02Ye", "rJdQIhER" ]
			retry_max = 913
			retry_max_wan = 23160
			rpc {
				enable_streaming = true
			}
			segment = "BC2NhTDi"
			segments = [
				{
					name = "PExYMe2E"
					bind = "36.73.36.19"
					port = 38295
					rpc_listener = true
					advertise = "63.39.19.18"
				},
				{
					name = "UzCvJgup"
					bind = "37.58.38.19"
					port = 39292
					rpc_listener = true
					advertise = "83.58.26.27"
				}
			]
			serf_lan = "99.43.63.15"
			serf_wan = "67.88.33.19"
			server = true
			server_name = "Oerr9n1G"
			service = {
				id = "dLOXpSCI"
				name = "o1ynPkp0"
				meta = {
					mymeta = "data"
				}
				tagged_addresses = {
					lan = {
						address = "2d79888a"
						port = 2143
					}
					wan = {
						address = "d4db85e2"
						port = 6109
					}
				}
				tags = ["nkwshvM5", "NTDWn3ek"]
				address = "cOlSOhbp"
				token = "msy7iWER"
				port = 24237
				weights = {
					passing = 100,
					warning = 1
				}
				enable_tag_override = true
				check = {
					id = "RMi85Dv8"
					name = "iehanzuq"
					status = "rCvn53TH"
					notes = "fti5lfF3"
					args = ["16WRUmwS", "QWk7j7ae"]
					http = "dl3Fgme3"
					header = {
						rjm4DEd3 = [ "2m3m2Fls" ]
						l4HwQ112 = [ "fk56MNlo", "dhLK56aZ" ]
					}
					method = "9afLm3Mj"
					body = "wVVL2V6f"
					tcp = "fjiLFqVd"
					interval = "23926s"
					docker_container_id = "dO5TtRHk"
					shell = "e6q2ttES"
					tls_skip_verify = true
					timeout = "38483s"
					ttl = "10943s"
					deregister_critical_service_after = "68787s"
				}
				checks = [
					{
						id = "Zv99e9Ka"
						name = "sgV4F7Pk"
						notes = "yP5nKbW0"
						status = "7oLMEyfu"
						args = ["5wEZtZpv", "0Ihyk8cS"]
						http = "KyDjGY9H"
						header = {
							"gv5qefTz" = [ "5Olo2pMG", "PvvKWQU5" ]
							"SHOVq1Vv" = [ "jntFhyym", "GYJh32pp" ]
						}
						method = "T66MFBfR"
						body = "OwGjTFQi"
						tcp = "bNnNfx2A"
						interval = "22224s"
						output_max_size = ` + strconv.Itoa(checks.DefaultBufSize) + `
						docker_container_id = "ipgdFtjd"
						shell = "omVZq7Sz"
						tls_skip_verify = true
						timeout = "18913s"
						ttl = "44743s"
						deregister_critical_service_after = "8482s"
					},
					{
						id = "G79O6Mpr"
						name = "IEqrzrsd"
						notes = "SVqApqeM"
						status = "XXkVoZXt"
						args = ["wD05Bvao", "rLYB7kQC"]
						http = "kyICZsn8"
						header = {
							"4ebP5vL4" = [ "G20SrL5Q", "DwPKlMbo" ]
							"p2UI34Qz" = [ "UsG1D0Qh", "NHhRiB6s" ]
						}
						method = "ciYHWors"
						body = "lUVLGYU7"
						tcp = "FfvCwlqH"
						interval = "12356s"
						output_max_size = ` + strconv.Itoa(checks.DefaultBufSize) + `
						docker_container_id = "HBndBU6R"
						shell = "hVI33JjA"
						tls_skip_verify = true
						timeout = "38282s"
						ttl = "1181s"
						deregister_critical_service_after = "4992s"
					}
				]
				connect {
					native = true
				}
			}
			services = [
				{
					id = "wI1dzxS4"
					name = "7IszXMQ1"
					tags = ["0Zwg8l6v", "zebELdN5"]
					address = "9RhqPSPB"
					token = "myjKJkWH"
					port = 72219
					enable_tag_override = true
					check = {
						id = "qmfeO5if"
						name = "atDGP7n5"
						status = "pDQKEhWL"
						notes = "Yt8EDLev"
						args = ["81EDZLPa", "bPY5X8xd"]
						http = "qzHYvmJO"
						header = {
							UkpmZ3a3 = [ "2dfzXuxZ" ]
							cVFpko4u = [ "gGqdEB6k", "9LsRo22u" ]
						}
						method = "X5DrovFc"
						body = "WeikigLh"
						tcp = "ICbxkpSF"
						interval = "24392s"
						output_max_size = ` + strconv.Itoa(checks.DefaultBufSize) + `
						docker_container_id = "ZKXr68Yb"
						shell = "CEfzx0Fo"
						tls_skip_verify = true
						timeout = "38333s"
						ttl = "57201s"
						deregister_critical_service_after = "44214s"
					}
					connect {
						sidecar_service {}
					}
				},
				{
					id = "MRHVMZuD"
					name = "6L6BVfgH"
					tags = ["7Ale4y6o", "PMBW08hy"]
					address = "R6H6g8h0"
					token = "ZgY8gjMI"
					port = 38292
					weights = {
						passing = 1979,
						warning = 6
					}
					enable_tag_override = true
					checks = [
						{
							id = "GTti9hCo"
							name = "9OOS93ne"
							notes = "CQy86DH0"
							status = "P0SWDvrk"
							args = ["EXvkYIuG", "BATOyt6h"]
							http = "u97ByEiW"
							header = {
								"MUlReo8L" = [ "AUZG7wHG", "gsN0Dc2N" ]
								"1UJXjVrT" = [ "OJgxzTfk", "xZZrFsq7" ]
							}
							method = "5wkAxCUE"
							body = "7CRjCJyz"
							tcp = "MN3oA9D2"
							interval = "32718s"
							output_max_size = ` + strconv.Itoa(checks.DefaultBufSize) + `
							docker_container_id = "cU15LMet"
							shell = "nEz9qz2l"
							tls_skip_verify = true
							timeout = "34738s"
							ttl = "22773s"
							deregister_critical_service_after = "84282s"
						},
						{
							id = "UHsDeLxG"
							name = "PQSaPWlT"
							notes = "jKChDOdl"
							status = "5qFz6OZn"
							args = ["NMtYWlT9", "vj74JXsm"]
							http = "1LBDJhw4"
							header = {
								"cXPmnv1M" = [ "imDqfaBx", "NFxZ1bQe" ],
								"vr7wY7CS" = [ "EtCoNPPL", "9vAarJ5s" ]
							}
							method = "wzByP903"
							body = "4I8ucZgZ"
							tcp = "2exjZIGE"
							interval = "5656s"
							output_max_size = ` + strconv.Itoa(checks.DefaultBufSize) + `
							docker_container_id = "5tDBWpfA"
							shell = "rlTpLM8s"
							tls_skip_verify = true
							timeout = "4868s"
							ttl = "11222s"
							deregister_critical_service_after = "68482s"
						}
					]
					connect {}
				},
				{
					id = "Kh81CPF6"
					name = "Kh81CPF6-proxy"
					port = 31471
					kind = "connect-proxy"
					proxy {
						destination_service_name = "6L6BVfgH"
						destination_service_id = "6L6BVfgH-id"
						local_service_address = "127.0.0.2"
						local_service_port = 23759
						config {
							cedGGtZf = "pWrUNiWw"
						}
						upstreams = [
							{
								destination_name = "KPtAj2cb"
								local_bind_port = 4051
								config {
									kzRnZOyd = "nUNKoL8H"
								}
							},
							{
								destination_type = "prepared_query"
								destination_namespace = "9nakw0td"
								destination_name = "KSd8HsRl"
								local_bind_port = 11884
								local_bind_address = "127.24.88.0"
							},
						]
						expose {
							checks = true
							paths = [
								{
									path = "/health"
									local_path_port = 8080
									listener_port = 21500
									protocol = "http"
								}
							]
						}
					}
				},
				{
					id = "kvVqbwSE"
					kind = "mesh-gateway"
					name = "gw-primary-dc"
					port = 27147
					proxy {
						config {
							"1CuJHVfw" = "Kzqsa7yc"
						}
					}
				}
			]
			session_ttl_min = "26627s"
			skip_leave_on_interrupt = true
			start_join = [ "LR3hGDoG", "MwVpZ4Up" ]
			start_join_wan = [ "EbFSc3nA", "kwXTh623" ]
			syslog_facility = "hHv79Uia"
			tagged_addresses = {
				"7MYgHrYH" = "dALJAhLD"
				"h6DdBy6K" = "ebrr9zZ8"
			}
			telemetry {
				circonus_api_app = "p4QOTe9j"
				circonus_api_token = "E3j35V23"
				circonus_api_url = "mEMjHpGg"
				circonus_broker_id = "BHlxUhed"
				circonus_broker_select_tag = "13xy1gHm"
				circonus_check_display_name = "DRSlQR6n"
				circonus_check_force_metric_activation = "Ua5FGVYf"
				circonus_check_id = "kGorutad"
				circonus_check_instance_id = "rwoOL6R4"
				circonus_check_search_tag = "ovT4hT4f"
				circonus_check_tags = "prvO4uBl"
				circonus_submission_interval = "DolzaflP"
				circonus_submission_url = "gTcbS93G"
				disable_hostname = true
				dogstatsd_addr = "0wSndumK"
				dogstatsd_tags = [ "3N81zSUB","Xtj8AnXZ" ]
				filter_default = true
				prefix_filter = [ "+oJotS8XJ","-cazlEhGn" ]
				metrics_prefix = "ftO6DySn"
				prometheus_retention_time = "15s"
				statsd_address = "drce87cy"
				statsite_address = "HpFwKB8R"
				disable_compat_1.9 = true
			}
			tls_cipher_suites = "TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256"
			tls_min_version = "pAOWafkR"
			tls_prefer_server_cipher_suites = true
			translate_wan_addrs = true
			ui_config {
				enabled = true
				dir = "pVncV4Ey"
				content_path = "qp1WRhYH"
				metrics_provider = "sgnaoa_lower_case"
				metrics_provider_files = ["sgnaMFoa", "dicnwkTH"]
				metrics_provider_options_json = "{\"DIbVQadX\": 1}"
				metrics_proxy {
					base_url = "http://foo.bar"
					add_headers = [
						{
							name = "p3nynwc9"
							value = "TYBgnN2F"
						}
					]
					path_allowlist = ["/aSh3cu", "/eiK/2Th"]
				}
			 	dashboard_url_templates {
					u2eziu2n_lower_case = "http://lkjasd.otr"
				}
			}
			unix_sockets = {
				group = "8pFodrV8"
				mode = "E8sAwOv4"
				user = "E0nB1DwA"
			}
			verify_incoming = true
			verify_incoming_https = true
			verify_incoming_rpc = true
			verify_outgoing = true
			verify_server_hostname = true
			watches = [{
				type = "key"
				datacenter = "GyE6jpeW"
				key = "j9lF1Tve"
				handler = "90N7S4LN"
			}, {
				type = "keyprefix"
				datacenter = "fYrl3F5d"
				key = "sl3Dffu7"
				args = ["dltjDJ2a", "flEa7C2d"]
			}]
		`}

	tail := map[string][]Source{
		"json": {
			FileSource{
				Name:   "tail.non-user.json",
				Format: "json",
				Data: `
				{
					"acl_disabled_ttl": "957s",
					"acl" : {
						"disabled_ttl" : "957s"
					},
					"ae_interval": "10003s",
					"check_deregister_interval_min": "27870s",
					"check_reap_interval": "10662s",
					"discovery_max_stale": "5s",
					"segment_limit": 24705,
					"segment_name_limit": 27046,
					"sync_coordinate_interval_min": "27983s",
					"sync_coordinate_rate_target": 137.81
				}`,
			},
			FileSource{
				Name:   "tail.consul.json",
				Format: "json",
				Data: `
				{
					"consul": {
						"coordinate": {
							"update_batch_size": 9244,
							"update_max_batches": 15164,
							"update_period": "25093s"
						},
						"raft": {
							"election_timeout": "31947s",
							"heartbeat_timeout": "25699s",
							"leader_lease_timeout": "15351s"
						},
						"server": {
							"health_interval": "17455s"
						}
					}
				}`,
			},
		},
		"hcl": {
			FileSource{
				Name:   "tail.non-user.hcl",
				Format: "hcl",
				Data: `
					acl_disabled_ttl = "957s"
					acl = {
						disabled_ttl = "957s"
					}
					ae_interval = "10003s"
					check_deregister_interval_min = "27870s"
					check_reap_interval = "10662s"
					discovery_max_stale = "5s"
					segment_limit = 24705
					segment_name_limit = 27046
					sync_coordinate_interval_min = "27983s"
					sync_coordinate_rate_target = 137.81
				`,
			},
			FileSource{
				Name:   "tail.consul.hcl",
				Format: "hcl",
				Data: `
					consul = {
						coordinate = {
							update_batch_size = 9244
							update_max_batches = 15164
							update_period = "25093s"
						}
						raft = {
							election_timeout = "31947s"
							heartbeat_timeout = "25699s"
							leader_lease_timeout = "15351s"
						}
						server = {
							health_interval = "17455s"
						}
					}
				`,
			},
		},
	}

	want := RuntimeConfig{
		// non-user configurable values
		ACLDisabledTTL:             957 * time.Second,
		AEInterval:                 10003 * time.Second,
		CheckDeregisterIntervalMin: 27870 * time.Second,
		CheckReapInterval:          10662 * time.Second,
		SegmentLimit:               24705,
		SegmentNameLimit:           27046,
		SyncCoordinateIntervalMin:  27983 * time.Second,
		SyncCoordinateRateTarget:   137.81,

		Revision:          "JNtPSav3",
		Version:           "R909Hblt",
		VersionPrerelease: "ZT1JOQLn",

		// consul configuration
		ConsulCoordinateUpdateBatchSize:  9244,
		ConsulCoordinateUpdateMaxBatches: 15164,
		ConsulCoordinateUpdatePeriod:     25093 * time.Second,
		ConsulRaftElectionTimeout:        5 * 31947 * time.Second,
		ConsulRaftHeartbeatTimeout:       5 * 25699 * time.Second,
		ConsulRaftLeaderLeaseTimeout:     5 * 15351 * time.Second,
		GossipLANGossipInterval:          25252 * time.Second,
		GossipLANGossipNodes:             6,
		GossipLANProbeInterval:           101 * time.Millisecond,
		GossipLANProbeTimeout:            102 * time.Millisecond,
		GossipLANSuspicionMult:           1235,
		GossipLANRetransmitMult:          1234,
		GossipWANGossipInterval:          6966 * time.Second,
		GossipWANGossipNodes:             2,
		GossipWANProbeInterval:           103 * time.Millisecond,
		GossipWANProbeTimeout:            104 * time.Millisecond,
		GossipWANSuspicionMult:           16385,
		GossipWANRetransmitMult:          16384,
		ConsulServerHealthInterval:       17455 * time.Second,

		// user configurable values

		ACLTokens: token.Config{
			EnablePersistence:   true,
			DataDir:             dataDir,
			ACLDefaultToken:     "418fdff1",
			ACLAgentToken:       "bed2377c",
			ACLAgentMasterToken: "64fd0e08",
			ACLReplicationToken: "5795983a",
		},

		ACLsEnabled:                      true,
		ACLDatacenter:                    "ejtmd43d",
		ACLDefaultPolicy:                 "72c2e7a0",
		ACLDownPolicy:                    "03eb2aee",
		ACLEnableKeyListPolicy:           true,
		ACLMasterToken:                   "8a19ac27",
		ACLTokenTTL:                      3321 * time.Second,
		ACLPolicyTTL:                     1123 * time.Second,
		ACLRoleTTL:                       9876 * time.Second,
		ACLTokenReplication:              true,
		AdvertiseAddrLAN:                 ipAddr("17.99.29.16"),
		AdvertiseAddrWAN:                 ipAddr("78.63.37.19"),
		AdvertiseReconnectTimeout:        0 * time.Second,
		AutopilotCleanupDeadServers:      true,
		AutopilotDisableUpgradeMigration: true,
		AutopilotLastContactThreshold:    12705 * time.Second,
		AutopilotMaxTrailingLogs:         17849,
		AutopilotMinQuorum:               3,
		AutopilotRedundancyZoneTag:       "3IsufDJf",
		AutopilotServerStabilizationTime: 23057 * time.Second,
		AutopilotUpgradeVersionTag:       "W9pDwFAL",
		BindAddr:                         ipAddr("16.99.34.17"),
		Bootstrap:                        true,
		BootstrapExpect:                  53,
		Cache: cache.Options{
			EntryFetchMaxBurst: 42,
			EntryFetchRate:     0.334,
		},
		CAFile:             "erA7T0PM",
		CAPath:             "mQEN1Mfp",
		CertFile:           "7s4QAzDk",
		CheckOutputMaxSize: checks.DefaultBufSize,
		Checks: []*structs.CheckDefinition{
			{
				ID:         "uAjE6m9Z",
				Name:       "QsZRGpYr",
				Notes:      "VJ7Sk4BY",
				ServiceID:  "lSulPcyz",
				Token:      "toO59sh8",
				Status:     "9RlWsXMV",
				ScriptArgs: []string{"4BAJttck", "4D2NPtTQ"},
				HTTP:       "dohLcyQ2",
				Header: map[string][]string{
					"ZBfTin3L": {"1sDbEqYG", "lJGASsWK"},
					"Ui0nU99X": {"LMccm3Qe", "k5H5RggQ"},
				},
				Method:                         "aldrIQ4l",
				Body:                           "wSjTy7dg",
				TCP:                            "RJQND605",
				Interval:                       22164 * time.Second,
				OutputMaxSize:                  checks.DefaultBufSize,
				DockerContainerID:              "ipgdFtjd",
				Shell:                          "qAeOYy0M",
				TLSSkipVerify:                  true,
				Timeout:                        1813 * time.Second,
				TTL:                            21743 * time.Second,
				DeregisterCriticalServiceAfter: 14232 * time.Second,
			},
			{
				ID:         "Cqq95BhP",
				Name:       "3qXpkS0i",
				Notes:      "sb5qLTex",
				ServiceID:  "CmUUcRna",
				Token:      "a3nQzHuy",
				Status:     "irj26nf3",
				ScriptArgs: []string{"9s526ogY", "gSlOHj1w"},
				HTTP:       "yzhgsQ7Y",
				Header: map[string][]string{
					"zcqwA8dO": {"qb1zx0DL", "sXCxPFsD"},
					"qxvdnSE9": {"6wBPUYdF", "YYh8wtSZ"},
				},
				Method:                         "gLrztrNw",
				Body:                           "0jkKgGUC",
				OutputMaxSize:                  checks.DefaultBufSize,
				TCP:                            "4jG5casb",
				Interval:                       28767 * time.Second,
				DockerContainerID:              "THW6u7rL",
				Shell:                          "C1Zt3Zwh",
				TLSSkipVerify:                  true,
				Timeout:                        18506 * time.Second,
				TTL:                            31006 * time.Second,
				DeregisterCriticalServiceAfter: 2366 * time.Second,
			},
			{
				ID:         "fZaCAXww",
				Name:       "OOM2eo0f",
				Notes:      "zXzXI9Gt",
				ServiceID:  "L8G0QNmR",
				Token:      "oo4BCTgJ",
				Status:     "qLykAl5u",
				ScriptArgs: []string{"f3BemRjy", "e5zgpef7"},
				HTTP:       "29B93haH",
				Header: map[string][]string{
					"hBq0zn1q": {"2a9o9ZKP", "vKwA5lR6"},
					"f3r6xFtM": {"RyuIdDWv", "QbxEcIUM"},
				},
				Method:                         "Dou0nGT5",
				Body:                           "5PBQd2OT",
				OutputMaxSize:                  checks.DefaultBufSize,
				TCP:                            "JY6fTTcw",
				Interval:                       18714 * time.Second,
				DockerContainerID:              "qF66POS9",
				Shell:                          "sOnDy228",
				TLSSkipVerify:                  true,
				Timeout:                        5954 * time.Second,
				TTL:                            30044 * time.Second,
				DeregisterCriticalServiceAfter: 13209 * time.Second,
			},
		},
		CheckUpdateInterval: 16507 * time.Second,
		ClientAddrs:         []*net.IPAddr{ipAddr("93.83.18.19")},
		ConfigEntryBootstrap: []structs.ConfigEntry{
			&structs.ProxyConfigEntry{
				Kind:           structs.ProxyDefaults,
				Name:           structs.ProxyConfigGlobal,
				EnterpriseMeta: *defaultEntMeta,
				Config: map[string]interface{}{
					"foo": "bar",
					// has to be a float due to being a map[string]interface
					"bar": float64(1),
				},
			},
		},
		AutoEncryptTLS:      false,
		AutoEncryptDNSSAN:   []string{"a.com", "b.com"},
		AutoEncryptIPSAN:    []net.IP{net.ParseIP("192.168.4.139"), net.ParseIP("192.168.4.140")},
		AutoEncryptAllowTLS: true,
		AutoConfig: AutoConfig{
			Enabled:         false,
			IntroToken:      "OpBPGRwt",
			IntroTokenFile:  "gFvAXwI8",
			DNSSANs:         []string{"6zdaWg9J"},
			IPSANs:          []net.IP{net.IPv4(198, 18, 99, 99)},
			ServerAddresses: []string{"198.18.100.1"},
			Authorizer: AutoConfigAuthorizer{
				Enabled:         true,
				AllowReuse:      true,
				ClaimAssertions: []string{"value.node == \"${node}\""},
				AuthMethod: structs.ACLAuthMethod{
					Name:           "Auto Config Authorizer",
					Type:           "jwt",
					EnterpriseMeta: *structs.DefaultEnterpriseMeta(),
					Config: map[string]interface{}{
						"JWTValidationPubKeys": []string{"-----BEGIN PUBLIC KEY-----\nMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAERVchfCZng4mmdvQz1+sJHRN40snC\nYt8NjYOnbnScEXMkyoUmASr88gb7jaVAVt3RYASAbgBjB2Z+EUizWkx5Tg==\n-----END PUBLIC KEY-----"},
						"ClaimMappings": map[string]string{
							"node": "node",
						},
						"BoundIssuer":    "consul",
						"BoundAudiences": []string{"consul-cluster-1"},
						"ListClaimMappings": map[string]string{
							"foo": "bar",
						},
						"OIDCDiscoveryURL":    "",
						"OIDCDiscoveryCACert": "",
						"JWKSURL":             "",
						"JWKSCACert":          "",
						"ExpirationLeeway":    0 * time.Second,
						"NotBeforeLeeway":     0 * time.Second,
						"ClockSkewLeeway":     0 * time.Second,
						"JWTSupportedAlgs":    []string(nil),
					},
				},
			},
		},
		ConnectEnabled:        true,
		ConnectSidecarMinPort: 8888,
		ConnectSidecarMaxPort: 9999,
		ExposeMinPort:         1111,
		ExposeMaxPort:         2222,
		ConnectCAProvider:     "consul",
		ConnectCAConfig: map[string]interface{}{
			"RotationPeriod":      "90h",
			"IntermediateCertTTL": "8760h",
			"LeafCertTTL":         "1h",
			"CSRMaxPerSecond":     float64(100),
			"CSRMaxConcurrent":    float64(2),
		},
		ConnectMeshGatewayWANFederationEnabled: false,
		DNSAddrs:                               []net.Addr{tcpAddr("93.95.95.81:7001"), udpAddr("93.95.95.81:7001")},
		DNSARecordLimit:                        29907,
		DNSAllowStale:                          true,
		DNSDisableCompression:                  true,
		DNSDomain:                              "7W1xXSqd",
		DNSAltDomain:                           "1789hsd",
		DNSEnableTruncate:                      true,
		DNSMaxStale:                            29685 * time.Second,
		DNSNodeTTL:                             7084 * time.Second,
		DNSOnlyPassing:                         true,
		DNSPort:                                7001,
		DNSRecursorTimeout:                     4427 * time.Second,
		DNSRecursors:                           []string{"63.38.39.58", "92.49.18.18"},
		DNSSOA:                                 RuntimeSOAConfig{Refresh: 3600, Retry: 600, Expire: 86400, Minttl: 0},
		DNSServiceTTL:                          map[string]time.Duration{"*": 32030 * time.Second},
		DNSUDPAnswerLimit:                      29909,
		DNSNodeMetaTXT:                         true,
		DNSUseCache:                            true,
		DNSCacheMaxAge:                         5 * time.Minute,
		DataDir:                                dataDir,
		Datacenter:                             "rzo029wg",
		DefaultQueryTime:                       16743 * time.Second,
		DevMode:                                true,
		DisableAnonymousSignature:              true,
		DisableCoordinates:                     true,
		DisableHostNodeID:                      true,
		DisableHTTPUnprintableCharFilter:       true,
		DisableKeyringFile:                     true,
		DisableRemoteExec:                      true,
		DisableUpdateCheck:                     true,
		DiscardCheckOutput:                     true,
		DiscoveryMaxStale:                      5 * time.Second,
		EnableAgentTLSForChecks:                true,
		EnableCentralServiceConfig:             false,
		EnableDebug:                            true,
		EnableRemoteScriptChecks:               true,
		EnableLocalScriptChecks:                true,
		EncryptKey:                             "A4wELWqH",
		EncryptVerifyIncoming:                  true,
		EncryptVerifyOutgoing:                  true,
		GRPCPort:                               4881,
		GRPCAddrs:                              []net.Addr{tcpAddr("32.31.61.91:4881")},
		HTTPAddrs:                              []net.Addr{tcpAddr("83.39.91.39:7999")},
		HTTPBlockEndpoints:                     []string{"RBvAFcGD", "fWOWFznh"},
		AllowWriteHTTPFrom:                     []*net.IPNet{cidr("127.0.0.0/8"), cidr("22.33.44.55/32"), cidr("0.0.0.0/0")},
		HTTPPort:                               7999,
		HTTPResponseHeaders:                    map[string]string{"M6TKa9NP": "xjuxjOzQ", "JRCrHZed": "rl0mTx81"},
		HTTPSAddrs:                             []net.Addr{tcpAddr("95.17.17.19:15127")},
		HTTPMaxConnsPerClient:                  100,
		HTTPMaxHeaderBytes:                     10,
		HTTPSHandshakeTimeout:                  2391 * time.Millisecond,
		HTTPSPort:                              15127,
		HTTPUseCache:                           false,
		KeyFile:                                "IEkkwgIA",
		KVMaxValueSize:                         1234567800000000,
		LeaveDrainTime:                         8265 * time.Second,
		LeaveOnTerm:                            true,
		Logging: logging.Config{
			LogLevel:       "k1zo9Spt",
			LogJSON:        true,
			EnableSyslog:   true,
			SyslogFacility: "hHv79Uia",
		},
		MaxQueryTime:            18237 * time.Second,
		NodeID:                  types.NodeID("AsUIlw99"),
		NodeMeta:                map[string]string{"5mgGQMBk": "mJLtVMSG", "A7ynFMJB": "0Nx6RGab"},
		NodeName:                "otlLxGaI",
		ReadReplica:             true,
		PidFile:                 "43xN80Km",
		PrimaryDatacenter:       "ejtmd43d",
		PrimaryGateways:         []string{"aej8eeZo", "roh2KahS"},
		PrimaryGatewaysInterval: 18866 * time.Second,
		RPCAdvertiseAddr:        tcpAddr("17.99.29.16:3757"),
		RPCBindAddr:             tcpAddr("16.99.34.17:3757"),
		RPCHandshakeTimeout:     1932 * time.Millisecond,
		RPCHoldTimeout:          15707 * time.Second,
		RPCProtocol:             30793,
		RPCRateLimit:            12029.43,
		RPCMaxBurst:             44848,
		RPCMaxConnsPerClient:    2954,
		RaftProtocol:            3,
		RaftSnapshotThreshold:   16384,
		RaftSnapshotInterval:    30 * time.Second,
		RaftTrailingLogs:        83749,
		ReconnectTimeoutLAN:     23739 * time.Second,
		ReconnectTimeoutWAN:     26694 * time.Second,
		RejoinAfterLeave:        true,
		RetryJoinIntervalLAN:    8067 * time.Second,
		RetryJoinIntervalWAN:    28866 * time.Second,
		RetryJoinLAN:            []string{"pbsSFY7U", "l0qLtWij"},
		RetryJoinMaxAttemptsLAN: 913,
		RetryJoinMaxAttemptsWAN: 23160,
		RetryJoinWAN:            []string{"PFsR02Ye", "rJdQIhER"},
		RPCConfig:               consul.RPCConfig{EnableStreaming: true},
		SegmentName:             "BC2NhTDi",
		Segments: []structs.NetworkSegment{
			{
				Name:        "PExYMe2E",
				Bind:        tcpAddr("36.73.36.19:38295"),
				Advertise:   tcpAddr("63.39.19.18:38295"),
				RPCListener: true,
			},
			{
				Name:        "UzCvJgup",
				Bind:        tcpAddr("37.58.38.19:39292"),
				Advertise:   tcpAddr("83.58.26.27:39292"),
				RPCListener: true,
			},
		},
		SerfPortLAN: 8301,
		SerfPortWAN: 8302,
		ServerMode:  true,
		ServerName:  "Oerr9n1G",
		ServerPort:  3757,
		Services: []*structs.ServiceDefinition{
			{
				ID:      "wI1dzxS4",
				Name:    "7IszXMQ1",
				Tags:    []string{"0Zwg8l6v", "zebELdN5"},
				Address: "9RhqPSPB",
				Token:   "myjKJkWH",
				Port:    72219,
				Weights: &structs.Weights{
					Passing: 1,
					Warning: 1,
				},
				EnableTagOverride: true,
				Checks: []*structs.CheckType{
					{
						CheckID:    "qmfeO5if",
						Name:       "atDGP7n5",
						Status:     "pDQKEhWL",
						Notes:      "Yt8EDLev",
						ScriptArgs: []string{"81EDZLPa", "bPY5X8xd"},
						HTTP:       "qzHYvmJO",
						Header: map[string][]string{
							"UkpmZ3a3": {"2dfzXuxZ"},
							"cVFpko4u": {"gGqdEB6k", "9LsRo22u"},
						},
						Method:                         "X5DrovFc",
						Body:                           "WeikigLh",
						OutputMaxSize:                  checks.DefaultBufSize,
						TCP:                            "ICbxkpSF",
						Interval:                       24392 * time.Second,
						DockerContainerID:              "ZKXr68Yb",
						Shell:                          "CEfzx0Fo",
						TLSSkipVerify:                  true,
						Timeout:                        38333 * time.Second,
						TTL:                            57201 * time.Second,
						DeregisterCriticalServiceAfter: 44214 * time.Second,
					},
				},
				// Note that although this SidecarService is only syntax sugar for
				// registering another service, that has to happen in the agent code so
				// it can make intelligent decisions about automatic port assignments
				// etc. So we expect config just to pass it through verbatim.
				Connect: &structs.ServiceConnect{
					SidecarService: &structs.ServiceDefinition{
						Weights: &structs.Weights{
							Passing: 1,
							Warning: 1,
						},
					},
				},
			},
			{
				ID:      "MRHVMZuD",
				Name:    "6L6BVfgH",
				Tags:    []string{"7Ale4y6o", "PMBW08hy"},
				Address: "R6H6g8h0",
				Token:   "ZgY8gjMI",
				Port:    38292,
				Weights: &structs.Weights{
					Passing: 1979,
					Warning: 6,
				},
				EnableTagOverride: true,
				Checks: structs.CheckTypes{
					&structs.CheckType{
						CheckID:    "GTti9hCo",
						Name:       "9OOS93ne",
						Notes:      "CQy86DH0",
						Status:     "P0SWDvrk",
						ScriptArgs: []string{"EXvkYIuG", "BATOyt6h"},
						HTTP:       "u97ByEiW",
						Header: map[string][]string{
							"MUlReo8L": {"AUZG7wHG", "gsN0Dc2N"},
							"1UJXjVrT": {"OJgxzTfk", "xZZrFsq7"},
						},
						Method:                         "5wkAxCUE",
						Body:                           "7CRjCJyz",
						OutputMaxSize:                  checks.DefaultBufSize,
						TCP:                            "MN3oA9D2",
						Interval:                       32718 * time.Second,
						DockerContainerID:              "cU15LMet",
						Shell:                          "nEz9qz2l",
						TLSSkipVerify:                  true,
						Timeout:                        34738 * time.Second,
						TTL:                            22773 * time.Second,
						DeregisterCriticalServiceAfter: 84282 * time.Second,
					},
					&structs.CheckType{
						CheckID:    "UHsDeLxG",
						Name:       "PQSaPWlT",
						Notes:      "jKChDOdl",
						Status:     "5qFz6OZn",
						ScriptArgs: []string{"NMtYWlT9", "vj74JXsm"},
						HTTP:       "1LBDJhw4",
						Header: map[string][]string{
							"cXPmnv1M": {"imDqfaBx", "NFxZ1bQe"},
							"vr7wY7CS": {"EtCoNPPL", "9vAarJ5s"},
						},
						Method:                         "wzByP903",
						Body:                           "4I8ucZgZ",
						OutputMaxSize:                  checks.DefaultBufSize,
						TCP:                            "2exjZIGE",
						Interval:                       5656 * time.Second,
						DockerContainerID:              "5tDBWpfA",
						Shell:                          "rlTpLM8s",
						TLSSkipVerify:                  true,
						Timeout:                        4868 * time.Second,
						TTL:                            11222 * time.Second,
						DeregisterCriticalServiceAfter: 68482 * time.Second,
					},
				},
				Connect: &structs.ServiceConnect{},
			},
			{
				ID:   "Kh81CPF6",
				Name: "Kh81CPF6-proxy",
				Port: 31471,
				Kind: "connect-proxy",
				Proxy: &structs.ConnectProxyConfig{
					DestinationServiceName: "6L6BVfgH",
					DestinationServiceID:   "6L6BVfgH-id",
					LocalServiceAddress:    "127.0.0.2",
					LocalServicePort:       23759,
					Config: map[string]interface{}{
						"cedGGtZf": "pWrUNiWw",
					},
					Upstreams: structs.Upstreams{
						{
							DestinationType: "service", // Default should be explicitly filled
							DestinationName: "KPtAj2cb",
							LocalBindPort:   4051,
							Config: map[string]interface{}{
								"kzRnZOyd": "nUNKoL8H",
							},
						},
						{
							DestinationType:      "prepared_query",
							DestinationNamespace: "9nakw0td",
							DestinationName:      "KSd8HsRl",
							LocalBindPort:        11884,
							LocalBindAddress:     "127.24.88.0",
						},
					},
					Expose: structs.ExposeConfig{
						Checks: true,
						Paths: []structs.ExposePath{
							{
								Path:          "/health",
								LocalPathPort: 8080,
								ListenerPort:  21500,
								Protocol:      "http",
							},
						},
					},
				},
				Weights: &structs.Weights{
					Passing: 1,
					Warning: 1,
				},
			},
			{
				ID:   "kvVqbwSE",
				Kind: "mesh-gateway",
				Name: "gw-primary-dc",
				Port: 27147,
				Proxy: &structs.ConnectProxyConfig{
					Config: map[string]interface{}{
						"1CuJHVfw": "Kzqsa7yc",
					},
					Upstreams: structs.Upstreams{},
				},
				Weights: &structs.Weights{
					Passing: 1,
					Warning: 1,
				},
			},
			{
				ID:   "dLOXpSCI",
				Name: "o1ynPkp0",
				TaggedAddresses: map[string]structs.ServiceAddress{
					"lan": {
						Address: "2d79888a",
						Port:    2143,
					},
					"wan": {
						Address: "d4db85e2",
						Port:    6109,
					},
				},
				Tags:    []string{"nkwshvM5", "NTDWn3ek"},
				Address: "cOlSOhbp",
				Token:   "msy7iWER",
				Meta:    map[string]string{"mymeta": "data"},
				Port:    24237,
				Weights: &structs.Weights{
					Passing: 100,
					Warning: 1,
				},
				EnableTagOverride: true,
				Connect: &structs.ServiceConnect{
					Native: true,
				},
				Checks: structs.CheckTypes{
					&structs.CheckType{
						CheckID:    "Zv99e9Ka",
						Name:       "sgV4F7Pk",
						Notes:      "yP5nKbW0",
						Status:     "7oLMEyfu",
						ScriptArgs: []string{"5wEZtZpv", "0Ihyk8cS"},
						HTTP:       "KyDjGY9H",
						Header: map[string][]string{
							"gv5qefTz": {"5Olo2pMG", "PvvKWQU5"},
							"SHOVq1Vv": {"jntFhyym", "GYJh32pp"},
						},
						Method:                         "T66MFBfR",
						Body:                           "OwGjTFQi",
						OutputMaxSize:                  checks.DefaultBufSize,
						TCP:                            "bNnNfx2A",
						Interval:                       22224 * time.Second,
						DockerContainerID:              "ipgdFtjd",
						Shell:                          "omVZq7Sz",
						TLSSkipVerify:                  true,
						Timeout:                        18913 * time.Second,
						TTL:                            44743 * time.Second,
						DeregisterCriticalServiceAfter: 8482 * time.Second,
					},
					&structs.CheckType{
						CheckID:    "G79O6Mpr",
						Name:       "IEqrzrsd",
						Notes:      "SVqApqeM",
						Status:     "XXkVoZXt",
						ScriptArgs: []string{"wD05Bvao", "rLYB7kQC"},
						HTTP:       "kyICZsn8",
						Header: map[string][]string{
							"4ebP5vL4": {"G20SrL5Q", "DwPKlMbo"},
							"p2UI34Qz": {"UsG1D0Qh", "NHhRiB6s"},
						},
						Method:                         "ciYHWors",
						Body:                           "lUVLGYU7",
						OutputMaxSize:                  checks.DefaultBufSize,
						TCP:                            "FfvCwlqH",
						Interval:                       12356 * time.Second,
						DockerContainerID:              "HBndBU6R",
						Shell:                          "hVI33JjA",
						TLSSkipVerify:                  true,
						Timeout:                        38282 * time.Second,
						TTL:                            1181 * time.Second,
						DeregisterCriticalServiceAfter: 4992 * time.Second,
					},
					&structs.CheckType{
						CheckID:    "RMi85Dv8",
						Name:       "iehanzuq",
						Status:     "rCvn53TH",
						Notes:      "fti5lfF3",
						ScriptArgs: []string{"16WRUmwS", "QWk7j7ae"},
						HTTP:       "dl3Fgme3",
						Header: map[string][]string{
							"rjm4DEd3": {"2m3m2Fls"},
							"l4HwQ112": {"fk56MNlo", "dhLK56aZ"},
						},
						Method:                         "9afLm3Mj",
						Body:                           "wVVL2V6f",
						OutputMaxSize:                  checks.DefaultBufSize,
						TCP:                            "fjiLFqVd",
						Interval:                       23926 * time.Second,
						DockerContainerID:              "dO5TtRHk",
						Shell:                          "e6q2ttES",
						TLSSkipVerify:                  true,
						Timeout:                        38483 * time.Second,
						TTL:                            10943 * time.Second,
						DeregisterCriticalServiceAfter: 68787 * time.Second,
					},
				},
			},
		},
		UseStreamingBackend:  true,
		SerfAdvertiseAddrLAN: tcpAddr("17.99.29.16:8301"),
		SerfAdvertiseAddrWAN: tcpAddr("78.63.37.19:8302"),
		SerfBindAddrLAN:      tcpAddr("99.43.63.15:8301"),
		SerfBindAddrWAN:      tcpAddr("67.88.33.19:8302"),
		SerfAllowedCIDRsLAN:  []net.IPNet{},
		SerfAllowedCIDRsWAN:  []net.IPNet{},
		SessionTTLMin:        26627 * time.Second,
		SkipLeaveOnInt:       true,
		StartJoinAddrsLAN:    []string{"LR3hGDoG", "MwVpZ4Up"},
		StartJoinAddrsWAN:    []string{"EbFSc3nA", "kwXTh623"},
		Telemetry: lib.TelemetryConfig{
			CirconusAPIApp:                     "p4QOTe9j",
			CirconusAPIToken:                   "E3j35V23",
			CirconusAPIURL:                     "mEMjHpGg",
			CirconusBrokerID:                   "BHlxUhed",
			CirconusBrokerSelectTag:            "13xy1gHm",
			CirconusCheckDisplayName:           "DRSlQR6n",
			CirconusCheckForceMetricActivation: "Ua5FGVYf",
			CirconusCheckID:                    "kGorutad",
			CirconusCheckInstanceID:            "rwoOL6R4",
			CirconusCheckSearchTag:             "ovT4hT4f",
			CirconusCheckTags:                  "prvO4uBl",
			CirconusSubmissionInterval:         "DolzaflP",
			CirconusSubmissionURL:              "gTcbS93G",
			DisableCompatOneNine:               true,
			DisableHostname:                    true,
			DogstatsdAddr:                      "0wSndumK",
			DogstatsdTags:                      []string{"3N81zSUB", "Xtj8AnXZ"},
			FilterDefault:                      true,
			AllowedPrefixes:                    []string{"oJotS8XJ"},
			BlockedPrefixes:                    []string{"cazlEhGn"},
			MetricsPrefix:                      "ftO6DySn",
			StatsdAddr:                         "drce87cy",
			StatsiteAddr:                       "HpFwKB8R",
			PrometheusOpts: prometheus.PrometheusOpts{
				Expiration: 15 * time.Second,
			},
		},
		TLSCipherSuites:             []uint16{tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA, tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256},
		TLSMinVersion:               "pAOWafkR",
		TLSPreferServerCipherSuites: true,
		TaggedAddresses: map[string]string{
			"7MYgHrYH": "dALJAhLD",
			"h6DdBy6K": "ebrr9zZ8",
			"lan":      "17.99.29.16",
			"lan_ipv4": "17.99.29.16",
			"wan":      "78.63.37.19",
			"wan_ipv4": "78.63.37.19",
		},
		TranslateWANAddrs: true,
		TxnMaxReqLen:      5678000000000000,
		UIConfig: UIConfig{
			Enabled:                    true,
			Dir:                        "pVncV4Ey",
			ContentPath:                "/qp1WRhYH/", // slashes are added in parsing
			MetricsProvider:            "sgnaoa_lower_case",
			MetricsProviderFiles:       []string{"sgnaMFoa", "dicnwkTH"},
			MetricsProviderOptionsJSON: "{\"DIbVQadX\": 1}",
			MetricsProxy: UIMetricsProxy{
				BaseURL: "http://foo.bar",
				AddHeaders: []UIMetricsProxyAddHeader{
					{
						Name:  "p3nynwc9",
						Value: "TYBgnN2F",
					},
				},
				PathAllowlist: []string{"/aSh3cu", "/eiK/2Th"},
			},
			DashboardURLTemplates: map[string]string{"u2eziu2n_lower_case": "http://lkjasd.otr"},
		},
		UnixSocketUser:       "E0nB1DwA",
		UnixSocketGroup:      "8pFodrV8",
		UnixSocketMode:       "E8sAwOv4",
		VerifyIncoming:       true,
		VerifyIncomingHTTPS:  true,
		VerifyIncomingRPC:    true,
		VerifyOutgoing:       true,
		VerifyServerHostname: true,
		Watches: []map[string]interface{}{
			{
				"type":       "key",
				"datacenter": "GyE6jpeW",
				"key":        "j9lF1Tve",
				"handler":    "90N7S4LN",
			},
			{
				"type":       "keyprefix",
				"datacenter": "fYrl3F5d",
				"key":        "sl3Dffu7",
				"args":       []interface{}{"dltjDJ2a", "flEa7C2d"},
			},
		},
	}

	entFullRuntimeConfig(&want)

	warns := []string{
		`The 'acl_datacenter' field is deprecated. Use the 'primary_datacenter' field instead.`,
		`bootstrap_expect > 0: expecting 53 servers`,
	}

	warns = append(warns, enterpriseConfigKeyWarnings...)

	// ensure that all fields are set to unique non-zero values
	// todo(fs): This currently fails since ServiceDefinition.Check is not used
	// todo(fs): not sure on how to work around this. Possible options are:
	// todo(fs):  * move first check into the Check field
	// todo(fs):  * ignore the Check field
	// todo(fs): both feel like a hack
	if err := nonZero("RuntimeConfig", nil, want); err != nil {
		t.Log(err)
	}

	for format, data := range src {
		t.Run(format, func(t *testing.T) {
			// parse the flags since this is the only way we can set the
			// DevMode flag
			var flags BuilderOpts
			fs := flag.NewFlagSet("", flag.ContinueOnError)
			AddFlags(fs, &flags)
			if err := fs.Parse(flagSrc); err != nil {
				t.Fatalf("ParseFlags: %s", err)
			}
			require.Len(t, fs.Args(), 0)

			b, err := NewBuilder(flags)
			if err != nil {
				t.Fatalf("NewBuilder: %s", err)
			}
			b.Sources = append(b.Sources, FileSource{Name: "full." + format, Data: data, Format: format})
			b.Tail = append(b.Tail, tail[format]...)
			b.Tail = append(b.Tail, versionSource("JNtPSav3", "R909Hblt", "ZT1JOQLn"))

			// construct the runtime config
			rt, err := b.Build()
			if err != nil {
				t.Fatalf("Build: %s", err)
			}

			require.Equal(t, want, rt)

			// at this point we have confirmed that the parsing worked
			// for all fields but the validation will fail since certain
			// combinations are not allowed. Since it is not possible to have
			// all fields with non-zero values and to have a valid configuration
			// we are patching a handful of safe fields to make validation pass.
			rt.Bootstrap = false
			rt.DevMode = false
			rt.UIConfig.Enabled = false
			rt.SegmentName = ""
			rt.Segments = nil

			// validate the runtime config
			if err := b.Validate(rt); err != nil {
				t.Fatalf("Validate: %s", err)
			}

			// check the warnings
			require.ElementsMatch(t, warns, b.Warnings, "Warnings: %#v", b.Warnings)
		})
	}
}

// nonZero verifies recursively that all fields are set to unique,
// non-zero and non-nil values.
//
// struct: check all fields recursively
// slice: check len > 0 and all values recursively
// ptr: check not nil
// bool: check not zero (cannot check uniqueness)
// string, int, uint: check not zero and unique
// other: error
func nonZero(name string, uniq map[interface{}]string, v interface{}) error {
	if v == nil {
		return fmt.Errorf("%q is nil", name)
	}

	if uniq == nil {
		uniq = map[interface{}]string{}
	}

	isUnique := func(v interface{}) error {
		if other := uniq[v]; other != "" {
			return fmt.Errorf("%q and %q both use value %q", name, other, v)
		}
		uniq[v] = name
		return nil
	}

	val, typ := reflect.ValueOf(v), reflect.TypeOf(v)
	// fmt.Printf("%s: %T\n", name, v)
	switch typ.Kind() {
	case reflect.Struct:
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			fieldname := fmt.Sprintf("%s.%s", name, f.Name)
			err := nonZero(fieldname, uniq, val.Field(i).Interface())
			if err != nil {
				return err
			}
		}

	case reflect.Slice:
		if val.Len() == 0 {
			return fmt.Errorf("%q is empty slice", name)
		}
		for i := 0; i < val.Len(); i++ {
			elemname := fmt.Sprintf("%s[%d]", name, i)
			err := nonZero(elemname, uniq, val.Index(i).Interface())
			if err != nil {
				return err
			}
		}

	case reflect.Map:
		if val.Len() == 0 {
			return fmt.Errorf("%q is empty map", name)
		}
		for _, key := range val.MapKeys() {
			keyname := fmt.Sprintf("%s[%s]", name, key.String())
			if err := nonZero(keyname, uniq, key.Interface()); err != nil {
				if strings.Contains(err.Error(), "is zero value") {
					return fmt.Errorf("%q has zero value map key", name)
				}
				return err
			}
			if err := nonZero(keyname, uniq, val.MapIndex(key).Interface()); err != nil {
				return err
			}
		}

	case reflect.Bool:
		if val.Bool() != true {
			return fmt.Errorf("%q is zero value", name)
		}
		// do not test bool for uniqueness since there are only two values

	case reflect.String:
		if val.Len() == 0 {
			return fmt.Errorf("%q is zero value", name)
		}
		return isUnique(v)

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if val.Int() == 0 {
			return fmt.Errorf("%q is zero value", name)
		}
		return isUnique(v)

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if val.Uint() == 0 {
			return fmt.Errorf("%q is zero value", name)
		}
		return isUnique(v)

	case reflect.Float32, reflect.Float64:
		if val.Float() == 0 {
			return fmt.Errorf("%q is zero value", name)
		}
		return isUnique(v)

	case reflect.Ptr:
		if val.IsNil() {
			return fmt.Errorf("%q is nil", name)
		}
		return nonZero("*"+name, uniq, val.Elem().Interface())

	default:
		return fmt.Errorf("%T is not supported", v)
	}
	return nil
}

func TestNonZero(t *testing.T) {
	var empty string

	tests := []struct {
		desc string
		v    interface{}
		err  error
	}{
		{"nil", nil, errors.New(`"x" is nil`)},
		{"zero bool", false, errors.New(`"x" is zero value`)},
		{"zero string", "", errors.New(`"x" is zero value`)},
		{"zero int", int(0), errors.New(`"x" is zero value`)},
		{"zero int8", int8(0), errors.New(`"x" is zero value`)},
		{"zero int16", int16(0), errors.New(`"x" is zero value`)},
		{"zero int32", int32(0), errors.New(`"x" is zero value`)},
		{"zero int64", int64(0), errors.New(`"x" is zero value`)},
		{"zero uint", uint(0), errors.New(`"x" is zero value`)},
		{"zero uint8", uint8(0), errors.New(`"x" is zero value`)},
		{"zero uint16", uint16(0), errors.New(`"x" is zero value`)},
		{"zero uint32", uint32(0), errors.New(`"x" is zero value`)},
		{"zero uint64", uint64(0), errors.New(`"x" is zero value`)},
		{"zero float32", float32(0), errors.New(`"x" is zero value`)},
		{"zero float64", float64(0), errors.New(`"x" is zero value`)},
		{"ptr to zero value", &empty, errors.New(`"*x" is zero value`)},
		{"empty slice", []string{}, errors.New(`"x" is empty slice`)},
		{"slice with zero value", []string{""}, errors.New(`"x[0]" is zero value`)},
		{"empty map", map[string]string{}, errors.New(`"x" is empty map`)},
		{"map with zero value key", map[string]string{"": "y"}, errors.New(`"x" has zero value map key`)},
		{"map with zero value elem", map[string]string{"y": ""}, errors.New(`"x[y]" is zero value`)},
		{"struct with nil field", struct{ Y *int }{}, errors.New(`"x.Y" is nil`)},
		{"struct with zero value field", struct{ Y string }{}, errors.New(`"x.Y" is zero value`)},
		{"struct with empty array", struct{ Y []string }{}, errors.New(`"x.Y" is empty slice`)},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			if got, want := nonZero("x", nil, tt.v), tt.err; !reflect.DeepEqual(got, want) {
				t.Fatalf("got error %v want %v", got, want)
			}
		})
	}
}

func TestConfigDecodeBytes(t *testing.T) {
	// Test with some input
	src := []byte("abc")
	key := base64.StdEncoding.EncodeToString(src)

	result, err := decodeBytes(key)
	if err != nil {
		t.Fatalf("err: %s", err)
	}

	if !bytes.Equal(src, result) {
		t.Fatalf("bad: %#v", result)
	}

	// Test with no input
	result, err = decodeBytes("")
	if err != nil {
		t.Fatalf("err: %s", err)
	}

	if len(result) > 0 {
		t.Fatalf("bad: %#v", result)
	}
}

func parseCIDR(t *testing.T, cidr string) *net.IPNet {
	_, x, err := net.ParseCIDR(cidr)
	if err != nil {
		t.Fatalf("CIDRParse: %v", err)
	}
	return x
}

func TestSanitize(t *testing.T) {
	rt := RuntimeConfig{
		BindAddr:             &net.IPAddr{IP: net.ParseIP("127.0.0.1")},
		CheckOutputMaxSize:   checks.DefaultBufSize,
		SerfAdvertiseAddrLAN: &net.TCPAddr{IP: net.ParseIP("1.2.3.4"), Port: 5678},
		DNSAddrs: []net.Addr{
			&net.TCPAddr{IP: net.ParseIP("1.2.3.4"), Port: 5678},
			&net.UDPAddr{IP: net.ParseIP("1.2.3.4"), Port: 5678},
		},
		DNSSOA: RuntimeSOAConfig{Refresh: 3600, Retry: 600, Expire: 86400, Minttl: 0},
		AllowWriteHTTPFrom: []*net.IPNet{
			parseCIDR(t, "127.0.0.0/8"),
			parseCIDR(t, "::1/128"),
		},
		HTTPAddrs: []net.Addr{
			&net.TCPAddr{IP: net.ParseIP("1.2.3.4"), Port: 5678},
			&net.UnixAddr{Name: "/var/run/foo"},
		},
		Cache: cache.Options{
			EntryFetchMaxBurst: 42,
			EntryFetchRate:     0.334,
		},
		ConsulCoordinateUpdatePeriod: 15 * time.Second,
		RaftProtocol:                 3,
		RetryJoinLAN: []string{
			"foo=bar key=baz secret=boom bang=bar",
		},
		RetryJoinWAN: []string{
			"wan_foo=bar wan_key=baz wan_secret=boom wan_bang=bar",
		},
		PrimaryGateways: []string{
			"pmgw_foo=bar pmgw_key=baz pmgw_secret=boom pmgw_bang=bar",
		},
		Services: []*structs.ServiceDefinition{
			{
				Name:  "foo",
				Token: "bar",
				Check: structs.CheckType{
					Name:          "blurb",
					OutputMaxSize: checks.DefaultBufSize,
				},
				Weights: &structs.Weights{
					Passing: 67,
					Warning: 3,
				},
			},
		},
		Checks: []*structs.CheckDefinition{
			{
				Name:          "zoo",
				Token:         "zope",
				OutputMaxSize: checks.DefaultBufSize,
			},
		},
		KVMaxValueSize: 1234567800000000,
		SerfAllowedCIDRsLAN: []net.IPNet{
			*parseCIDR(t, "192.168.1.0/24"),
			*parseCIDR(t, "127.0.0.0/8"),
		},
		TxnMaxReqLen: 5678000000000000,
		UIConfig: UIConfig{
			MetricsProxy: UIMetricsProxy{
				AddHeaders: []UIMetricsProxyAddHeader{
					{Name: "foo", Value: "secret"},
				},
			},
		},
	}

	rtJSON := `{
		"ACLTokens": {
			` + entTokenConfigSanitize + `
			"ACLAgentMasterToken": "hidden",
			"ACLAgentToken": "hidden",
			"ACLDefaultToken": "hidden",
			"ACLReplicationToken": "hidden",
			"DataDir": "",
			"EnablePersistence": false
		},
		"ACLDatacenter": "",
		"ACLDefaultPolicy": "",
		"ACLDisabledTTL": "0s",
		"ACLDownPolicy": "",
		"ACLEnableKeyListPolicy": false,
		"ACLMasterToken": "hidden",
		"ACLPolicyTTL": "0s",
		"ACLRoleTTL": "0s",
		"ACLTokenReplication": false,
		"ACLTokenTTL": "0s",
		"ACLsEnabled": false,
		"AEInterval": "0s",
		"AdvertiseAddrLAN": "",
		"AdvertiseAddrWAN": "",
		"AdvertiseReconnectTimeout": "0s",
		"AutopilotCleanupDeadServers": false,
		"AutopilotDisableUpgradeMigration": false,
		"AutopilotLastContactThreshold": "0s",
		"AutopilotMaxTrailingLogs": 0,
		"AutopilotMinQuorum": 0,
		"AutopilotRedundancyZoneTag": "",
		"AutopilotServerStabilizationTime": "0s",
		"AutopilotUpgradeVersionTag": "",
		"BindAddr": "127.0.0.1",
		"Bootstrap": false,
		"BootstrapExpect": 0,
		"Cache": {
			"EntryFetchMaxBurst": 42,
			"EntryFetchRate": 0.334
		},
		"CAFile": "",
		"CAPath": "",
		"CertFile": "",
		"CheckDeregisterIntervalMin": "0s",
		"CheckOutputMaxSize": ` + strconv.Itoa(checks.DefaultBufSize) + `,
		"CheckReapInterval": "0s",
		"CheckUpdateInterval": "0s",
		"Checks": [{
			"AliasNode": "",
			"AliasService": "",
			"DeregisterCriticalServiceAfter": "0s",
			"DockerContainerID": "",
			"EnterpriseMeta": ` + entMetaJSON + `,
			"SuccessBeforePassing": 0,
			"FailuresBeforeCritical": 0,
			"GRPC": "",
			"GRPCUseTLS": false,
			"HTTP": "",
			"Header": {},
			"ID": "",
			"Interval": "0s",
			"Method": "",
			"Body": "",
			"Name": "zoo",
			"Notes": "",
			"OutputMaxSize": ` + strconv.Itoa(checks.DefaultBufSize) + `,
			"ScriptArgs": [],
			"ServiceID": "",
			"Shell": "",
			"Status": "",
			"TCP": "",
			"TLSSkipVerify": false,
			"TTL": "0s",
			"Timeout": "0s",
			"Token": "hidden"
		}],
		"ClientAddrs": [],
		"ConfigEntryBootstrap": [],
		"AutoEncryptTLS": false,
		"AutoEncryptDNSSAN": [],
		"AutoEncryptIPSAN": [],
		"AutoEncryptAllowTLS": false,
		"ConnectCAConfig": {},
		"ConnectCAProvider": "",
		"ConnectEnabled": false,
		"ConnectMeshGatewayWANFederationEnabled": false,
		"ConnectSidecarMaxPort": 0,
		"ConnectSidecarMinPort": 0,
		"ConnectTestCALeafRootChangeSpread": "0s",
		"ConsulCoordinateUpdateBatchSize": 0,
		"ConsulCoordinateUpdateMaxBatches": 0,
		"ConsulCoordinateUpdatePeriod": "15s",
		"ConsulRaftElectionTimeout": "0s",
		"CheckOutputMaxSize": ` + strconv.Itoa(checks.DefaultBufSize) + `,
		"ConsulRaftHeartbeatTimeout": "0s",
		"ConsulRaftLeaderLeaseTimeout": "0s",
		"GossipLANGossipInterval": "0s",
		"GossipLANGossipNodes": 0,
		"GossipLANProbeInterval": "0s",
		"GossipLANProbeTimeout": "0s",
		"GossipLANRetransmitMult": 0,
		"GossipLANSuspicionMult": 0,
		"GossipWANGossipInterval": "0s",
		"GossipWANGossipNodes": 0,
		"GossipWANProbeInterval": "0s",
		"GossipWANProbeTimeout": "0s",
		"GossipWANRetransmitMult": 0,
		"GossipWANSuspicionMult": 0,
		"ConsulServerHealthInterval": "0s",
		"DNSARecordLimit": 0,
		"DNSAddrs": [
			"tcp://1.2.3.4:5678",
			"udp://1.2.3.4:5678"
		],
		"DNSAllowStale": false,
		"DNSDisableCompression": false,
		"DNSDomain": "",
		"DNSAltDomain": "",
		"DNSEnableTruncate": false,
		"DNSMaxStale": "0s",
		"DNSNodeMetaTXT": false,
		"DNSNodeTTL": "0s",
		"DNSOnlyPassing": false,
		"DNSPort": 0,
		"DNSRecursorTimeout": "0s",
		"DNSRecursors": [],
		"DNSServiceTTL": {},
		"DNSSOA": {
			"Refresh": 3600,
			"Retry": 600,
			"Expire": 86400,
			"Minttl": 0
		},
		"DNSUDPAnswerLimit": 0,
		"DNSUseCache": false,
		"DNSCacheMaxAge": "0s",
		"DataDir": "",
		"Datacenter": "",
		"DefaultQueryTime": "0s",
		"DevMode": false,
		"DisableAnonymousSignature": false,
		"DisableCoordinates": false,
		"DisableHTTPUnprintableCharFilter": false,
		"DisableHostNodeID": false,
		"DisableKeyringFile": false,
		"DisableRemoteExec": false,
		"DisableUpdateCheck": false,
		"DiscardCheckOutput": false,
		"DiscoveryMaxStale": "0s",
		"EnableAgentTLSForChecks": false,
		"EnableDebug": false,
		"EnableCentralServiceConfig": false,
		"EnableLocalScriptChecks": false,
		"EnableRemoteScriptChecks": false,
		"EncryptKey": "hidden",
		"EncryptVerifyIncoming": false,
		"EncryptVerifyOutgoing": false,
		"EnterpriseRuntimeConfig": ` + entRuntimeConfigSanitize + `,
		"ExposeMaxPort": 0,
		"ExposeMinPort": 0,
		"GRPCAddrs": [],
		"GRPCPort": 0,
		"HTTPAddrs": [
			"tcp://1.2.3.4:5678",
			"unix:///var/run/foo"
		],
		"HTTPBlockEndpoints": [],
		"HTTPMaxConnsPerClient": 0,
		"HTTPMaxHeaderBytes": 0,
		"HTTPPort": 0,
		"HTTPResponseHeaders": {},
		"HTTPUseCache": false,
		"HTTPSAddrs": [],
		"HTTPSHandshakeTimeout": "0s",
		"HTTPSPort": 0,
		"KeyFile": "hidden",
		"KVMaxValueSize": 1234567800000000,
		"LeaveDrainTime": "0s",
		"LeaveOnTerm": false,
		"Logging": {
			"EnableSyslog": false,
			"LogLevel": "",
			"LogJSON": false,
			"LogFilePath": "",
			"LogRotateBytes": 0,
			"LogRotateDuration": "0s",
			"LogRotateMaxFiles": 0,
			"Name": "",
			"SyslogFacility": ""
		},
		"MaxQueryTime": "0s",
		"NodeID": "",
		"NodeMeta": {},
		"NodeName": "",
		"PidFile": "",
		"PrimaryDatacenter": "",
		"PrimaryGateways": [
			"pmgw_foo=bar pmgw_key=baz pmgw_secret=boom pmgw_bang=bar"
		],
		"PrimaryGatewaysInterval": "0s",
		"ReadReplica": false,
		"RPCAdvertiseAddr": "",
		"RPCBindAddr": "",
		"RPCHandshakeTimeout": "0s",
		"RPCHoldTimeout": "0s",
		"RPCMaxBurst": 0,
		"RPCMaxConnsPerClient": 0,
		"RPCProtocol": 0,
		"RPCRateLimit": 0,
		"RPCConfig": {
			"EnableStreaming": false
		},
		"RaftProtocol": 3,
		"RaftSnapshotInterval": "0s",
		"RaftSnapshotThreshold": 0,
		"RaftTrailingLogs": 0,
		"ReconnectTimeoutLAN": "0s",
		"ReconnectTimeoutWAN": "0s",
		"RejoinAfterLeave": false,
		"RetryJoinIntervalLAN": "0s",
		"RetryJoinIntervalWAN": "0s",
		"RetryJoinLAN": [
			"foo=bar key=hidden secret=hidden bang=bar"
		],
		"RetryJoinMaxAttemptsLAN": 0,
		"RetryJoinMaxAttemptsWAN": 0,
		"RetryJoinWAN": [
			"wan_foo=bar wan_key=hidden wan_secret=hidden wan_bang=bar"
		],
		"Revision": "",
		"SegmentLimit": 0,
		"SegmentName": "",
		"SegmentNameLimit": 0,
		"Segments": [],
		"SerfAdvertiseAddrLAN": "tcp://1.2.3.4:5678",
		"SerfAdvertiseAddrWAN": "",
		"SerfAllowedCIDRsLAN": ["192.168.1.0/24", "127.0.0.0/8"],
		"SerfAllowedCIDRsWAN": [],
		"SerfBindAddrLAN": "",
		"SerfBindAddrWAN": "",
		"SerfPortLAN": 0,
		"SerfPortWAN": 0,
		"UseStreamingBackend": false,
		"ServerMode": false,
		"ServerName": "",
		"ServerPort": 0,
		"Services": [{
			"Address": "",
			"Check": {
				"AliasNode": "",
				"AliasService": "",
				"CheckID": "",
				"DeregisterCriticalServiceAfter": "0s",
				"DockerContainerID": "",
				"SuccessBeforePassing": 0,
				"FailuresBeforeCritical": 0,
				"GRPC": "",
				"GRPCUseTLS": false,
				"HTTP": "",
				"Header": {},
				"Interval": "0s",
				"Method": "",
				"Body": "",
				"Name": "blurb",
				"Notes": "",
				"OutputMaxSize": ` + strconv.Itoa(checks.DefaultBufSize) + `,
				"ProxyGRPC": "",
				"ProxyHTTP": "",
				"ScriptArgs": [],
				"Shell": "",
				"Status": "",
				"TCP": "",
				"TLSSkipVerify": false,
				"TTL": "0s",
				"Timeout": "0s"
			},
			"Checks": [],
			"Connect": null,
			"EnableTagOverride": false,
			"EnterpriseMeta": ` + entMetaJSON + `,
			"ID": "",
			"Kind": "",
			"Meta": {},
			"Name": "foo",
			"Port": 0,
			"Proxy": null,
			"TaggedAddresses": {},
			"Tags": [],
			"Token": "hidden",
			"Weights": {
				"Passing": 67,
				"Warning": 3
			}
		}],
		"SessionTTLMin": "0s",
		"SkipLeaveOnInt": false,
		"StartJoinAddrsLAN": [],
		"StartJoinAddrsWAN": [],
		"SyncCoordinateIntervalMin": "0s",
		"SyncCoordinateRateTarget": 0,
		"TLSCipherSuites": [],
		"TLSMinVersion": "",
		"TLSPreferServerCipherSuites": false,
		"TaggedAddresses": {},
		"Telemetry": {
			"AllowedPrefixes": [],
			"BlockedPrefixes": [],
			"CirconusAPIApp": "",
			"CirconusAPIToken": "hidden",
			"CirconusAPIURL": "",
			"CirconusBrokerID": "",
			"CirconusBrokerSelectTag": "",
			"CirconusCheckDisplayName": "",
			"CirconusCheckForceMetricActivation": "",
			"CirconusCheckID": "",
			"CirconusCheckInstanceID": "",
			"CirconusCheckSearchTag": "",
			"CirconusCheckTags": "",
			"CirconusSubmissionInterval": "",
			"CirconusSubmissionURL": "",
			"Disable": false,
			"DisableCompatOneNine": false,
			"DisableHostname": false,
			"DogstatsdAddr": "",
			"DogstatsdTags": [],
			"FilterDefault": false,
			"MetricsPrefix": "",
			"StatsdAddr": "",
			"StatsiteAddr": "",
			"PrometheusOpts": {
				"Expiration": "0s",
				"Registerer": null,
				"GaugeDefinitions": [],
				"CounterDefinitions": [],
				"SummaryDefinitions": []
			}
		},
		"TranslateWANAddrs": false,
		"TxnMaxReqLen": 5678000000000000,
		"UIConfig": {
			"ContentPath": "",
			"Dir": "",
			"Enabled": false,
			"MetricsProvider": "",
			"MetricsProviderFiles": [],
			"MetricsProviderOptionsJSON": "",
			"MetricsProxy": {
				"AddHeaders": [
					{
						"Name": "foo",
						"Value": "hidden"
					}
				],
				"BaseURL": "",
				"PathAllowlist": []
			},
			"DashboardURLTemplates": {}
		},
		"UnixSocketGroup": "",
		"UnixSocketMode": "",
		"UnixSocketUser": "",
		"VerifyIncoming": false,
		"VerifyIncomingHTTPS": false,
		"VerifyIncomingRPC": false,
		"VerifyOutgoing": false,
		"VerifyServerHostname": false,
		"Version": "",
		"VersionPrerelease": "",
		"Watches": [],
		"AllowWriteHTTPFrom": [
			"127.0.0.0/8",
			"::1/128"
		],
		"AutoConfig": {
			"Authorizer": {
				"Enabled": false,
				"AllowReuse": false,
				"AuthMethod": {
					"ACLAuthMethodEnterpriseFields": ` + authMethodEntFields + `,
					"Config": {},
					"Description": "",
					"DisplayName": "",
					"EnterpriseMeta": ` + entMetaJSON + `,
					"MaxTokenTTL": "0s",
					"Name": "",
					"RaftIndex": {
						"CreateIndex": 0,
						"ModifyIndex": 0
					},
					"Type": "",
					"TokenLocality": ""			
				},
				"ClaimAssertions": []
			},
			"Enabled": false,
			"DNSSANs": [],
			"IntroToken": "hidden",
			"IntroTokenFile": "",
			"IPSANs": [],
			"ServerAddresses": []
		}
	}`
	b, err := json.MarshalIndent(rt.Sanitized(), "", "    ")
	if err != nil {
		t.Fatal(err)
	}
	require.JSONEq(t, rtJSON, string(b))
}

func TestRuntime_apiAddresses(t *testing.T) {
	rt := RuntimeConfig{
		HTTPAddrs: []net.Addr{
			&net.TCPAddr{IP: net.ParseIP("198.18.0.1"), Port: 5678},
			&net.UnixAddr{Name: "/var/run/foo"},
		},
		HTTPSAddrs: []net.Addr{
			&net.TCPAddr{IP: net.ParseIP("198.18.0.2"), Port: 5678},
		}}

	unixAddrs, httpAddrs, httpsAddrs := rt.apiAddresses(1)

	require.Len(t, unixAddrs, 1)
	require.Len(t, httpAddrs, 1)
	require.Len(t, httpsAddrs, 1)

	require.Equal(t, "/var/run/foo", unixAddrs[0])
	require.Equal(t, "198.18.0.1:5678", httpAddrs[0])
	require.Equal(t, "198.18.0.2:5678", httpsAddrs[0])
}

func TestRuntime_APIConfigHTTPS(t *testing.T) {
	rt := RuntimeConfig{
		HTTPAddrs: []net.Addr{
			&net.TCPAddr{IP: net.ParseIP("198.18.0.1"), Port: 5678},
			&net.UnixAddr{Name: "/var/run/foo"},
		},
		HTTPSAddrs: []net.Addr{
			&net.TCPAddr{IP: net.ParseIP("198.18.0.2"), Port: 5678},
		},
		Datacenter:     "dc-test",
		CAFile:         "/etc/consul/ca.crt",
		CAPath:         "/etc/consul/ca.dir",
		CertFile:       "/etc/consul/server.crt",
		KeyFile:        "/etc/consul/ssl/server.key",
		VerifyOutgoing: false,
	}

	cfg, err := rt.APIConfig(false)
	require.NoError(t, err)
	require.Equal(t, "198.18.0.2:5678", cfg.Address)
	require.Equal(t, "https", cfg.Scheme)
	require.Equal(t, rt.CAFile, cfg.TLSConfig.CAFile)
	require.Equal(t, rt.CAPath, cfg.TLSConfig.CAPath)
	require.Equal(t, "", cfg.TLSConfig.CertFile)
	require.Equal(t, "", cfg.TLSConfig.KeyFile)
	require.Equal(t, rt.Datacenter, cfg.Datacenter)
	require.Equal(t, true, cfg.TLSConfig.InsecureSkipVerify)

	rt.VerifyOutgoing = true
	cfg, err = rt.APIConfig(true)
	require.NoError(t, err)
	require.Equal(t, "198.18.0.2:5678", cfg.Address)
	require.Equal(t, "https", cfg.Scheme)
	require.Equal(t, rt.CAFile, cfg.TLSConfig.CAFile)
	require.Equal(t, rt.CAPath, cfg.TLSConfig.CAPath)
	require.Equal(t, rt.CertFile, cfg.TLSConfig.CertFile)
	require.Equal(t, rt.KeyFile, cfg.TLSConfig.KeyFile)
	require.Equal(t, rt.Datacenter, cfg.Datacenter)
	require.Equal(t, false, cfg.TLSConfig.InsecureSkipVerify)
}

func TestRuntime_APIConfigHTTP(t *testing.T) {
	rt := RuntimeConfig{
		HTTPAddrs: []net.Addr{
			&net.UnixAddr{Name: "/var/run/foo"},
			&net.TCPAddr{IP: net.ParseIP("198.18.0.1"), Port: 5678},
		},
		Datacenter: "dc-test",
	}

	cfg, err := rt.APIConfig(false)
	require.NoError(t, err)
	require.Equal(t, rt.Datacenter, cfg.Datacenter)
	require.Equal(t, "198.18.0.1:5678", cfg.Address)
	require.Equal(t, "http", cfg.Scheme)
	require.Equal(t, "", cfg.TLSConfig.CAFile)
	require.Equal(t, "", cfg.TLSConfig.CAPath)
	require.Equal(t, "", cfg.TLSConfig.CertFile)
	require.Equal(t, "", cfg.TLSConfig.KeyFile)
}

func TestRuntime_APIConfigUNIX(t *testing.T) {
	rt := RuntimeConfig{
		HTTPAddrs: []net.Addr{
			&net.UnixAddr{Name: "/var/run/foo"},
		},
		Datacenter: "dc-test",
	}

	cfg, err := rt.APIConfig(false)
	require.NoError(t, err)
	require.Equal(t, rt.Datacenter, cfg.Datacenter)
	require.Equal(t, "unix:///var/run/foo", cfg.Address)
	require.Equal(t, "http", cfg.Scheme)
	require.Equal(t, "", cfg.TLSConfig.CAFile)
	require.Equal(t, "", cfg.TLSConfig.CAPath)
	require.Equal(t, "", cfg.TLSConfig.CertFile)
	require.Equal(t, "", cfg.TLSConfig.KeyFile)
}

func TestRuntime_APIConfigANYAddrV4(t *testing.T) {
	rt := RuntimeConfig{
		HTTPAddrs: []net.Addr{
			&net.TCPAddr{IP: net.ParseIP("0.0.0.0"), Port: 5678},
		},
		Datacenter: "dc-test",
	}

	cfg, err := rt.APIConfig(false)
	require.NoError(t, err)
	require.Equal(t, rt.Datacenter, cfg.Datacenter)
	require.Equal(t, "127.0.0.1:5678", cfg.Address)
	require.Equal(t, "http", cfg.Scheme)
	require.Equal(t, "", cfg.TLSConfig.CAFile)
	require.Equal(t, "", cfg.TLSConfig.CAPath)
	require.Equal(t, "", cfg.TLSConfig.CertFile)
	require.Equal(t, "", cfg.TLSConfig.KeyFile)
}

func TestRuntime_APIConfigANYAddrV6(t *testing.T) {
	rt := RuntimeConfig{
		HTTPAddrs: []net.Addr{
			&net.TCPAddr{IP: net.ParseIP("::"), Port: 5678},
		},
		Datacenter: "dc-test",
	}

	cfg, err := rt.APIConfig(false)
	require.NoError(t, err)
	require.Equal(t, rt.Datacenter, cfg.Datacenter)
	require.Equal(t, "[::1]:5678", cfg.Address)
	require.Equal(t, "http", cfg.Scheme)
	require.Equal(t, "", cfg.TLSConfig.CAFile)
	require.Equal(t, "", cfg.TLSConfig.CAPath)
	require.Equal(t, "", cfg.TLSConfig.CertFile)
	require.Equal(t, "", cfg.TLSConfig.KeyFile)
}

func TestRuntime_ClientAddress(t *testing.T) {
	rt := RuntimeConfig{
		HTTPAddrs: []net.Addr{
			&net.TCPAddr{IP: net.ParseIP("::"), Port: 5678},
			&net.TCPAddr{IP: net.ParseIP("198.18.0.1"), Port: 5679},
			&net.UnixAddr{Name: "/var/run/foo", Net: "unix"},
		},
		HTTPSAddrs: []net.Addr{
			&net.TCPAddr{IP: net.ParseIP("::"), Port: 5688},
			&net.TCPAddr{IP: net.ParseIP("198.18.0.1"), Port: 5689},
		},
	}

	unix, http, https := rt.ClientAddress()

	require.Equal(t, "unix:///var/run/foo", unix)
	require.Equal(t, "198.18.0.1:5679", http)
	require.Equal(t, "198.18.0.1:5689", https)
}

func TestRuntime_ClientAddressAnyV4(t *testing.T) {
	rt := RuntimeConfig{
		HTTPAddrs: []net.Addr{
			&net.TCPAddr{IP: net.ParseIP("0.0.0.0"), Port: 5678},
			&net.UnixAddr{Name: "/var/run/foo", Net: "unix"},
		},
		HTTPSAddrs: []net.Addr{
			&net.TCPAddr{IP: net.ParseIP("0.0.0.0"), Port: 5688},
		},
	}

	unix, http, https := rt.ClientAddress()

	require.Equal(t, "unix:///var/run/foo", unix)
	require.Equal(t, "127.0.0.1:5678", http)
	require.Equal(t, "127.0.0.1:5688", https)
}

func TestRuntime_ClientAddressAnyV6(t *testing.T) {
	rt := RuntimeConfig{
		HTTPAddrs: []net.Addr{
			&net.TCPAddr{IP: net.ParseIP("::"), Port: 5678},
			&net.UnixAddr{Name: "/var/run/foo", Net: "unix"},
		},
		HTTPSAddrs: []net.Addr{
			&net.TCPAddr{IP: net.ParseIP("::"), Port: 5688},
		},
	}

	unix, http, https := rt.ClientAddress()

	require.Equal(t, "unix:///var/run/foo", unix)
	require.Equal(t, "[::1]:5678", http)
	require.Equal(t, "[::1]:5688", https)
}

func TestRuntime_ToTLSUtilConfig(t *testing.T) {
	c := &RuntimeConfig{
		VerifyIncoming:              true,
		VerifyIncomingRPC:           true,
		VerifyIncomingHTTPS:         true,
		VerifyOutgoing:              true,
		VerifyServerHostname:        true,
		CAFile:                      "a",
		CAPath:                      "b",
		CertFile:                    "c",
		KeyFile:                     "d",
		NodeName:                    "e",
		ServerName:                  "f",
		DNSDomain:                   "g",
		TLSMinVersion:               "tls12",
		TLSCipherSuites:             []uint16{tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA},
		TLSPreferServerCipherSuites: true,
		EnableAgentTLSForChecks:     true,
		AutoEncryptTLS:              true,
	}
	r := c.ToTLSUtilConfig()
	require.True(t, r.VerifyIncoming)
	require.True(t, r.VerifyIncomingRPC)
	require.True(t, r.VerifyIncomingHTTPS)
	require.True(t, r.VerifyOutgoing)
	require.True(t, r.EnableAgentTLSForChecks)
	require.True(t, r.AutoTLS)
	require.True(t, r.VerifyServerHostname)
	require.True(t, r.PreferServerCipherSuites)
	require.Equal(t, "a", r.CAFile)
	require.Equal(t, "b", r.CAPath)
	require.Equal(t, "c", r.CertFile)
	require.Equal(t, "d", r.KeyFile)
	require.Equal(t, "e", r.NodeName)
	require.Equal(t, "f", r.ServerName)
	require.Equal(t, "g", r.Domain)
	require.Equal(t, "tls12", r.TLSMinVersion)
	require.Equal(t, []uint16{tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA}, r.CipherSuites)
}

func TestRuntime_ToTLSUtilConfig_AutoConfig(t *testing.T) {
	c := &RuntimeConfig{
		VerifyIncoming:              true,
		VerifyIncomingRPC:           true,
		VerifyIncomingHTTPS:         true,
		VerifyOutgoing:              true,
		VerifyServerHostname:        true,
		CAFile:                      "a",
		CAPath:                      "b",
		CertFile:                    "c",
		KeyFile:                     "d",
		NodeName:                    "e",
		ServerName:                  "f",
		DNSDomain:                   "g",
		TLSMinVersion:               "tls12",
		TLSCipherSuites:             []uint16{tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA},
		TLSPreferServerCipherSuites: true,
		EnableAgentTLSForChecks:     true,
		AutoConfig:                  AutoConfig{Enabled: true},
	}
	r := c.ToTLSUtilConfig()
	require.True(t, r.VerifyIncoming)
	require.True(t, r.VerifyIncomingRPC)
	require.True(t, r.VerifyIncomingHTTPS)
	require.True(t, r.VerifyOutgoing)
	require.True(t, r.EnableAgentTLSForChecks)
	require.True(t, r.AutoTLS)
	require.True(t, r.VerifyServerHostname)
	require.True(t, r.PreferServerCipherSuites)
	require.Equal(t, "a", r.CAFile)
	require.Equal(t, "b", r.CAPath)
	require.Equal(t, "c", r.CertFile)
	require.Equal(t, "d", r.KeyFile)
	require.Equal(t, "e", r.NodeName)
	require.Equal(t, "f", r.ServerName)
	require.Equal(t, "g", r.Domain)
	require.Equal(t, "tls12", r.TLSMinVersion)
	require.Equal(t, []uint16{tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA}, r.CipherSuites)
}

func Test_UIPathBuilder(t *testing.T) {
	cases := []struct {
		name     string
		path     string
		expected string
	}{
		{
			"Letters only string",
			"hello",
			"/hello/",
		},
		{
			"Alphanumeric",
			"Hello1",
			"/Hello1/",
		},
		{
			"Hyphen and underscore",
			"-_",
			"/-_/",
		},
		{
			"Many slashes",
			"/hello/ui/1/",
			"/hello/ui/1/",
		},
	}

	for _, tt := range cases {
		actual := UIPathBuilder(tt.path)
		require.Equal(t, tt.expected, actual)

	}
}

func splitIPPort(hostport string) (net.IP, int) {
	h, p, err := net.SplitHostPort(hostport)
	if err != nil {
		panic(err)
	}
	port, err := strconv.Atoi(p)
	if err != nil {
		panic(err)
	}
	return net.ParseIP(h), port
}

func ipAddr(addr string) *net.IPAddr {
	return &net.IPAddr{IP: net.ParseIP(addr)}
}

func tcpAddr(addr string) *net.TCPAddr {
	ip, port := splitIPPort(addr)
	return &net.TCPAddr{IP: ip, Port: port}
}

func udpAddr(addr string) *net.UDPAddr {
	ip, port := splitIPPort(addr)
	return &net.UDPAddr{IP: ip, Port: port}
}

func unixAddr(addr string) *net.UnixAddr {
	if !strings.HasPrefix(addr, "unix://") {
		panic("not a unix socket addr: " + addr)
	}
	return &net.UnixAddr{Net: "unix", Name: addr[len("unix://"):]}
}

func writeFile(path string, data []byte) {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		panic(err)
	}
	if err := ioutil.WriteFile(path, data, 0640); err != nil {
		panic(err)
	}
}

func cleanDir(path string) {
	root := path
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if path == root {
			return nil
		}
		return os.RemoveAll(path)
	})
	if err != nil {
		panic(err)
	}
}

func randomString(n int) string {
	s := ""
	for ; n > 0; n-- {
		s += "x"
	}
	return s
}

func metaPairs(n int, format string) string {
	var s []string
	for i := 0; i < n; i++ {
		switch format {
		case "json":
			s = append(s, fmt.Sprintf(`"%d":"%d"`, i, i))
		case "hcl":
			s = append(s, fmt.Sprintf(`"%d"="%d"`, i, i))
		default:
			panic("invalid format: " + format)
		}
	}
	switch format {
	case "json":
		return strings.Join(s, ",")
	case "hcl":
		return strings.Join(s, " ")
	default:
		panic("invalid format: " + format)
	}
}

func TestConnectCAConfiguration(t *testing.T) {
	type testCase struct {
		config   RuntimeConfig
		expected *structs.CAConfiguration
		err      string
	}

	cases := map[string]testCase{
		"defaults": {
			config: RuntimeConfig{
				ConnectEnabled: true,
			},
			expected: &structs.CAConfiguration{
				Provider: "consul",
				Config: map[string]interface{}{
					"RotationPeriod":      "2160h",
					"LeafCertTTL":         "72h",
					"IntermediateCertTTL": "8760h", // 365 * 24h
				},
			},
		},
		"cluster-id-override": {
			config: RuntimeConfig{
				ConnectEnabled: true,
				ConnectCAConfig: map[string]interface{}{
					"cluster_id": "adfe7697-09b4-413a-ac0a-fa81ed3a3001",
				},
			},
			expected: &structs.CAConfiguration{
				Provider:  "consul",
				ClusterID: "adfe7697-09b4-413a-ac0a-fa81ed3a3001",
				Config: map[string]interface{}{
					"RotationPeriod":      "2160h",
					"LeafCertTTL":         "72h",
					"IntermediateCertTTL": "8760h", // 365 * 24h
					"cluster_id":          "adfe7697-09b4-413a-ac0a-fa81ed3a3001",
				},
			},
		},
		"cluster-id-non-uuid": {
			config: RuntimeConfig{
				ConnectEnabled: true,
				ConnectCAConfig: map[string]interface{}{
					"cluster_id": "foo",
				},
			},
			err: "cluster_id was supplied but was not a valid UUID",
		},
		"provider-override": {
			config: RuntimeConfig{
				ConnectEnabled:    true,
				ConnectCAProvider: "vault",
			},
			expected: &structs.CAConfiguration{
				Provider: "vault",
				Config: map[string]interface{}{
					"RotationPeriod":      "2160h",
					"LeafCertTTL":         "72h",
					"IntermediateCertTTL": "8760h", // 365 * 24h
				},
			},
		},
		"other-config": {
			config: RuntimeConfig{
				ConnectEnabled: true,
				ConnectCAConfig: map[string]interface{}{
					"foo": "bar",
				},
			},
			expected: &structs.CAConfiguration{
				Provider: "consul",
				Config: map[string]interface{}{
					"RotationPeriod":      "2160h",
					"LeafCertTTL":         "72h",
					"IntermediateCertTTL": "8760h", // 365 * 24h
					"foo":                 "bar",
				},
			},
		},
	}

	for name, tcase := range cases {
		t.Run(name, func(t *testing.T) {
			actual, err := tcase.config.ConnectCAConfiguration()
			if tcase.err != "" {
				testutil.RequireErrorContains(t, err, tcase.err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tcase.expected, actual)
			}
		})
	}
}
