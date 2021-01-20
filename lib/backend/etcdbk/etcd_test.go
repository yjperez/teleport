/*
Copyright 2015-2018 Gravitational, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package etcdbk

import (
	"context"
	"encoding/base64"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gravitational/teleport/lib/backend"
	"github.com/gravitational/teleport/lib/backend/test"
	"github.com/gravitational/teleport/lib/utils"

	"github.com/gravitational/trace"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"
	"go.etcd.io/etcd/clientv3"
	"gopkg.in/check.v1"
)

const (
	customPrefix1 = "/custom/"
	customPrefix2 = "/teleport-new/"
	customPrefix3 = "/teleport-new"
)

func TestEtcd(t *testing.T) { check.TestingT(t) }

type EtcdSuite struct {
	bk     *EtcdBackend
	suite  test.BackendSuite
	config backend.Params
}

var _ = check.Suite(&EtcdSuite{})

func (s *EtcdSuite) SetUpSuite(c *check.C) {
	utils.InitLoggerForTests(testing.Verbose())

	// this config must match examples/etcd/teleport.yaml
	s.config = backend.Params{
		"peers":         []string{"https://127.0.0.1:2379"},
		"prefix":        "/teleport",
		"tls_key_file":  "../../../examples/etcd/certs/client-key.pem",
		"tls_cert_file": "../../../examples/etcd/certs/client-cert.pem",
		"tls_ca_file":   "../../../examples/etcd/certs/ca-cert.pem",
		"dial_timeout":  500 * time.Millisecond,
	}

	newBackend := func() (backend.Backend, error) {
		return New(context.Background(), s.config)
	}
	s.suite.NewBackend = newBackend
}

func (s *EtcdSuite) SetUpTest(c *check.C) {
	if !etcdTestEnabled() {
		c.Skip("This test requires etcd, start it with examples/etcd/start-etcd.sh and set TELEPORT_ETCD_TEST=yes")
	}
	// Initiate a backend with a registry
	b, err := s.suite.NewBackend()
	c.Assert(err, check.IsNil)
	s.bk = b.(*EtcdBackend)
	s.suite.B = s.bk

	s.bk.cfg.Key = legacyDefaultPrefix
}

func (s *EtcdSuite) TearDownTest(c *check.C) {
	if s.bk == nil {
		return
	}
	s.reset(c)
	err := s.bk.Close()
	c.Assert(err, check.IsNil)
}

func (s *EtcdSuite) reset(c *check.C) {
	ctx := context.Background()
	_, err := s.bk.client.Delete(ctx, strings.TrimSuffix(legacyDefaultPrefix, "/"), clientv3.WithPrefix())
	c.Assert(err, check.IsNil)
	_, err = s.bk.client.Delete(ctx, strings.TrimSuffix(customPrefix1, "/"), clientv3.WithPrefix())
	c.Assert(err, check.IsNil)
	_, err = s.bk.client.Delete(ctx, strings.TrimSuffix(customPrefix2, "/"), clientv3.WithPrefix())
	c.Assert(err, check.IsNil)
	_, err = s.bk.client.Delete(ctx, strings.TrimSuffix(customPrefix3, "/"), clientv3.WithPrefix())
	c.Assert(err, check.IsNil)
}

func (s *EtcdSuite) TestCRUD(c *check.C) {
	s.suite.CRUD(c)
}

func (s *EtcdSuite) TestRange(c *check.C) {
	s.suite.Range(c)
}

func (s *EtcdSuite) TestDeleteRange(c *check.C) {
	s.suite.DeleteRange(c)
}

func (s *EtcdSuite) TestCompareAndSwap(c *check.C) {
	s.suite.CompareAndSwap(c)
}

func (s *EtcdSuite) TestExpiration(c *check.C) {
	s.suite.Expiration(c)
}

func (s *EtcdSuite) TestEvents(c *check.C) {
	s.suite.Events(c)
}

func (s *EtcdSuite) TestWatchersClose(c *check.C) {
	s.suite.WatchersClose(c)
}

func (s *EtcdSuite) TestLocking(c *check.C) {
	s.suite.Locking(c)
}

func (s *EtcdSuite) TestPrefix(c *check.C) {
	s.bk.cfg.Key = customPrefix1
	c.Assert(s.bk.cfg.Validate(), check.IsNil)

	var (
		ctx  = context.Background()
		item = backend.Item{
			Key:   []byte("/foo"),
			Value: []byte("bar"),
		}
	)

	// Item key starts with '/'.
	_, err := s.bk.Put(ctx, item)
	c.Assert(err, check.IsNil)

	wantKey := s.bk.cfg.Key + string(item.Key)
	s.assertKV(ctx, c, wantKey, string(item.Value))
	got, err := s.bk.Get(ctx, item.Key)
	c.Assert(err, check.IsNil)
	item.ID = got.ID
	c.Assert(*got, check.DeepEquals, item)

	// Item key does not start with '/'.
	item = backend.Item{
		Key:   []byte("foo"),
		Value: []byte("bar"),
	}
	_, err = s.bk.Put(ctx, item)
	c.Assert(err, check.IsNil)

	wantKey = s.bk.cfg.Key + string(item.Key)
	s.assertKV(ctx, c, wantKey, string(item.Value))
	got, err = s.bk.Get(ctx, item.Key)
	c.Assert(err, check.IsNil)
	item.ID = got.ID
	c.Assert(*got, check.DeepEquals, item)
}

func (s *EtcdSuite) assertKV(ctx context.Context, c *check.C, key, val string) {
	c.Logf("assert that key %q contains value %q", key, val)
	resp, err := s.bk.client.Get(ctx, key)
	c.Assert(err, check.IsNil)
	c.Assert(len(resp.Kvs), check.Equals, 1)
	c.Assert(string(resp.Kvs[0].Key), check.Equals, key)
	// Note: EtcdBackend stores all values base64-encoded.
	gotValue, err := base64.StdEncoding.DecodeString(string(resp.Kvs[0].Value))
	c.Assert(err, check.IsNil)
	c.Assert(string(gotValue), check.Equals, val)
}

func (s *EtcdSuite) TestSyncLegacyPrefix(c *check.C) {
	// Stop the watch goroutine to allow us to modify s.bk.cfg.Key without data
	// races.
	s.bk.cancel()
	<-s.bk.watchDone

	ctx := context.Background()
	s.bk.cfg.Key = customPrefix1
	c.Assert(s.bk.cfg.Validate(), check.IsNil)
	s.bk.clock = clockwork.NewFakeClock()

	snapshot := func() map[string]string {
		kvs := make(map[string]string)

		resp, err := s.bk.client.Get(ctx, strings.TrimSuffix(legacyDefaultPrefix, "/"), clientv3.WithPrefix())
		c.Assert(err, check.IsNil)
		for _, kv := range resp.Kvs {
			kvs[string(kv.Key)] = string(kv.Value)
		}

		resp, err = s.bk.client.Get(ctx, strings.TrimSuffix(customPrefix1, "/"), clientv3.WithPrefix())
		c.Assert(err, check.IsNil)
		for _, kv := range resp.Kvs {
			kvs[string(kv.Key)] = string(kv.Value)
		}

		resp, err = s.bk.client.Get(ctx, strings.TrimSuffix(customPrefix2, "/"), clientv3.WithPrefix())
		c.Assert(err, check.IsNil)
		for _, kv := range resp.Kvs {
			kvs[string(kv.Key)] = string(kv.Value)
		}

		resp, err = s.bk.client.Get(ctx, strings.TrimSuffix(customPrefix3, "/"), clientv3.WithPrefix())
		c.Assert(err, check.IsNil)
		for _, kv := range resp.Kvs {
			kvs[string(kv.Key)] = string(kv.Value)
		}

		return kvs
	}

	// Make sure the prefixes start off clean.
	s.reset(c)

	c.Log("both prefixes empty")
	err := s.bk.syncLegacyPrefix(ctx)
	c.Assert(err, check.IsNil)
	c.Assert(snapshot(), check.DeepEquals, map[string]string{})
	s.reset(c)

	c.Log("data in custom prefix, no data in legacy prefix; custom prefix should be preserved")
	_, err = s.bk.client.Put(ctx, customPrefix1+"/0", "c0")
	c.Assert(err, check.IsNil)
	_, err = s.bk.client.Put(ctx, customPrefix1+"/1", "c1")
	c.Assert(err, check.IsNil)
	err = s.bk.syncLegacyPrefix(ctx)
	c.Assert(err, check.IsNil)
	c.Assert(snapshot(), check.DeepEquals, map[string]string{
		customPrefix1 + "/0": "c0",
		customPrefix1 + "/1": "c1",
	})
	s.reset(c)

	c.Log("no data in custom prefix, data in legacy prefix copied over; custom prefix should be populated")
	_, err = s.bk.client.Put(ctx, legacyDefaultPrefix+"0", "l0")
	c.Assert(err, check.IsNil)
	_, err = s.bk.client.Put(ctx, legacyDefaultPrefix+"1", "l1")
	c.Assert(err, check.IsNil)
	err = s.bk.syncLegacyPrefix(ctx)
	c.Assert(err, check.IsNil)
	c.Assert(snapshot(), check.DeepEquals, map[string]string{
		legacyDefaultPrefix + "0": "l0",
		legacyDefaultPrefix + "1": "l1",
		customPrefix1 + "/0":      "l0",
		customPrefix1 + "/1":      "l1",
	})
	s.reset(c)

	c.Log("data in both prefixes, custom prefix is newer; custom prefix should be preserved")
	_, err = s.bk.client.Put(ctx, legacyDefaultPrefix+"0", "l0")
	c.Assert(err, check.IsNil)
	_, err = s.bk.client.Put(ctx, legacyDefaultPrefix+"1", "l1")
	c.Assert(err, check.IsNil)
	_, err = s.bk.client.Put(ctx, legacyDefaultPrefix+"3", "l3")
	c.Assert(err, check.IsNil)
	_, err = s.bk.client.Put(ctx, customPrefix1+"/0", "c0")
	c.Assert(err, check.IsNil)
	_, err = s.bk.client.Put(ctx, customPrefix1+"/1", "c1")
	c.Assert(err, check.IsNil)
	_, err = s.bk.client.Put(ctx, customPrefix1+"/2", "c2")
	c.Assert(err, check.IsNil)
	err = s.bk.syncLegacyPrefix(ctx)
	c.Assert(err, check.IsNil)
	c.Assert(snapshot(), check.DeepEquals, map[string]string{
		legacyDefaultPrefix + "0": "l0",
		legacyDefaultPrefix + "1": "l1",
		legacyDefaultPrefix + "3": "l3",
		customPrefix1 + "/0":      "c0",
		customPrefix1 + "/1":      "c1",
		customPrefix1 + "/2":      "c2",
	})
	s.reset(c)

	c.Log("data in both prefixes, legacy prefix is newer; custom prefix should be replaced")
	backupPrefix := s.bk.backupPrefix(customPrefix1)
	_, err = s.bk.client.Put(ctx, legacyDefaultPrefix+"0", "l0")
	c.Assert(err, check.IsNil)
	_, err = s.bk.client.Put(ctx, legacyDefaultPrefix+"1", "l1")
	c.Assert(err, check.IsNil)
	_, err = s.bk.client.Put(ctx, customPrefix1+"/0", "c0")
	c.Assert(err, check.IsNil)
	_, err = s.bk.client.Put(ctx, customPrefix1+"/1", "c1")
	c.Assert(err, check.IsNil)
	_, err = s.bk.client.Put(ctx, customPrefix1+"/2", "c2")
	c.Assert(err, check.IsNil)
	_, err = s.bk.client.Put(ctx, legacyDefaultPrefix+"3", "l3")
	c.Assert(err, check.IsNil)
	err = s.bk.syncLegacyPrefix(ctx)
	c.Assert(err, check.IsNil)
	c.Assert(snapshot(), check.DeepEquals, map[string]string{
		legacyDefaultPrefix + "0": "l0",
		legacyDefaultPrefix + "1": "l1",
		legacyDefaultPrefix + "3": "l3",
		customPrefix1 + "/0":      "l0",
		customPrefix1 + "/1":      "l1",
		customPrefix1 + "/3":      "l3",
		backupPrefix + "/0":       "c0",
		backupPrefix + "/1":       "c1",
		backupPrefix + "/2":       "c2",
	})
	s.reset(c)

	c.Log("data in custom prefix that partially matches the legacy prefix, no data in legacy prefix; custom prefix should be preserved")
	_, err = s.bk.client.Put(ctx, customPrefix2+"/0", "c0")
	c.Assert(err, check.IsNil)
	_, err = s.bk.client.Put(ctx, customPrefix2+"/1", "c1")
	c.Assert(err, check.IsNil)
	err = s.bk.syncLegacyPrefix(ctx)
	c.Assert(err, check.IsNil)
	c.Assert(snapshot(), check.DeepEquals, map[string]string{
		customPrefix2 + "/0": "c0",
		customPrefix2 + "/1": "c1",
	})
	s.reset(c)

	c.Log("data in both prefixes, legacy prefix is newer; custom prefix without trailing slash; custom prefix should be replaced")
	s.bk.cfg.Key = customPrefix3
	backupPrefix = s.bk.backupPrefix(customPrefix3)
	_, err = s.bk.client.Put(ctx, legacyDefaultPrefix+"0", "l0")
	c.Assert(err, check.IsNil)
	_, err = s.bk.client.Put(ctx, legacyDefaultPrefix+"1", "l1")
	c.Assert(err, check.IsNil)
	_, err = s.bk.client.Put(ctx, customPrefix3+"/0", "c0")
	c.Assert(err, check.IsNil)
	_, err = s.bk.client.Put(ctx, customPrefix3+"/1", "c1")
	c.Assert(err, check.IsNil)
	_, err = s.bk.client.Put(ctx, customPrefix3+"/2", "c2")
	c.Assert(err, check.IsNil)
	_, err = s.bk.client.Put(ctx, legacyDefaultPrefix+"3", "l3")
	c.Assert(err, check.IsNil)
	err = s.bk.syncLegacyPrefix(ctx)
	c.Assert(err, check.IsNil)
	c.Assert(snapshot(), check.DeepEquals, map[string]string{
		legacyDefaultPrefix + "0": "l0",
		legacyDefaultPrefix + "1": "l1",
		legacyDefaultPrefix + "3": "l3",
		customPrefix3 + "/0":      "l0",
		customPrefix3 + "/1":      "l1",
		customPrefix3 + "/3":      "l3",
		backupPrefix + "/0":       "c0",
		backupPrefix + "/1":       "c1",
		backupPrefix + "/2":       "c2",
	})
	s.reset(c)
}

// TestCompareAndSwapOversizedValue ensures that the backend reacts with a proper
// error message if client sends a message exceeding the configured size maximum
// See https://github.com/gravitational/teleport/issues/4786
func TestCompareAndSwapOversizedValue(t *testing.T) {
	if !etcdTestEnabled() {
		t.Skip("This test requires etcd, start it with examples/etcd/start-etcd.sh and set TELEPORT_ETCD_TEST=yes")
	}
	// setup
	const maxClientMsgSize = 128
	bk, err := New(context.Background(), backend.Params{
		"peers":                          []string{"https://127.0.0.1:2379"},
		"prefix":                         "/teleport",
		"tls_key_file":                   "../../../examples/etcd/certs/client-key.pem",
		"tls_cert_file":                  "../../../examples/etcd/certs/client-cert.pem",
		"tls_ca_file":                    "../../../examples/etcd/certs/ca-cert.pem",
		"dial_timeout":                   500 * time.Millisecond,
		"etcd_max_client_msg_size_bytes": maxClientMsgSize,
	})
	require.NoError(t, err)
	defer bk.Close()
	prefix := test.MakePrefix()
	// Explicitly exceed the message size
	value := make([]byte, maxClientMsgSize+1)

	// verify
	_, err = bk.CompareAndSwap(context.Background(),
		backend.Item{Key: prefix("one"), Value: []byte("1")},
		backend.Item{Key: prefix("one"), Value: value},
	)
	require.True(t, trace.IsLimitExceeded(err))
	require.Regexp(t, ".*ResourceExhausted.*", err)
}

func TestKeepAlives(t *testing.T) {
	bk := newBackend(t)
	defer bk.Close()

	watcher := bk.newWatcher(t, context.TODO())
	defer watcher.Close()

	expiresAt := addSeconds(bk.clock.Now(), 2)
	item, lease := bk.addItem(t, context.TODO(), "key", "val1", expiresAt)

	bk.clock.Advance(1 * time.Second)

	// Move the expiration further in the future to avoid processing
	// skew and ensure the item is available when we delete it.
	// It does not affect the running time of the test
	updatedAt := addSeconds(bk.clock.Now(), 60)
	err := bk.KeepAlive(context.TODO(), lease, updatedAt)
	require.NoError(t, err)

	// Consider expiration timestamps with a skew (see below for an explanation)
	compareExpires := cmp.FilterPath(func(p cmp.Path) bool {
		return p.String() == "Item.Expires"
	}, cmpopts.EquateApproxTime(1*time.Second))

	// Since the backend translates absolute expiration timestamp to a TTL
	// and collecting events takes arbitrary time, the expiration timestamps
	// on the collected events might have a slight skew
	events := collectEvents(t, watcher, 3)
	require.Empty(t, cmp.Diff(events, []backend.Event{
		{Type: backend.OpInit, Item: backend.Item{}},
		{Type: backend.OpPut, Item: backend.Item{Key: bk.prefix("key"), Value: []byte("val1"), Expires: expiresAt}},
		{Type: backend.OpPut, Item: backend.Item{Key: bk.prefix("key"), Value: []byte("val1"), Expires: updatedAt}},
	}, cmpopts.IgnoreFields(backend.Item{}, "ID"), compareExpires))

	err = bk.Delete(context.TODO(), item.Key)
	require.NoError(t, err)

	_, err = bk.Get(context.TODO(), item.Key)
	require.IsType(t, trace.NotFound(""), err)

	// keep alive on deleted or expired object should fail
	err = bk.KeepAlive(context.TODO(), lease, updatedAt.Add(1*time.Second))
	require.IsType(t, trace.NotFound(""), err)
}

func newBackend(t *testing.T) pack {
	bk, err := New(context.Background(), backend.Params{
		"peers":         []string{"https://127.0.0.1:2379"},
		"prefix":        "/teleport",
		"tls_key_file":  "../../../examples/etcd/certs/client-key.pem",
		"tls_cert_file": "../../../examples/etcd/certs/client-cert.pem",
		"tls_ca_file":   "../../../examples/etcd/certs/ca-cert.pem",
		"dial_timeout":  500 * time.Millisecond,
	})
	require.NoError(t, err)
	clock := clockwork.NewFakeClock()
	bk.clock = clock
	return pack{
		EtcdBackend: bk,
		prefix:      test.MakePrefix(),
		clock:       clock,
	}
}

func (r pack) addItem(t *testing.T, ctx context.Context, key, value string, expires time.Time) (backend.Item, backend.Lease) {
	item := backend.Item{
		Key:     r.prefix(key),
		Value:   []byte(value),
		Expires: expires,
	}
	lease, err := r.Put(ctx, item)
	require.NoError(t, err)
	return item, *lease
}

func (r pack) newWatcher(t *testing.T, ctx context.Context) backend.Watcher {
	watcher, err := r.NewWatcher(ctx, backend.Watch{Prefixes: [][]byte{r.prefix("")}})
	require.NoError(t, err)
	return watcher
}

type pack struct {
	*EtcdBackend
	prefix func(key string) []byte
	clock  clockwork.FakeClock
}

func collectEvents(t *testing.T, w backend.Watcher, count int) (events []backend.Event) {
	for i := 0; i < count; i++ {
		select {
		case e := <-w.Events():
			events = append(events, e)
		case <-w.Done():
			t.Fatal("Watcher has unexpectedly closed.")
		}
	}
	return events
}

// addSeconds adds seconds with a seconds precission
// always rounding up to the next second,
// because TTL engines are usually 1 second precision
func addSeconds(now time.Time, seconds int64) time.Time {
	return time.Unix(now.Unix()+seconds+1, 0)
}

func etcdTestEnabled() bool {
	return os.Getenv("TELEPORT_ETCD_TEST") != ""
}
