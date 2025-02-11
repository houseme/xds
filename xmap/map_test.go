package xmap

import (
	"bytes"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	_ "net/http/pprof"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/heiyeluren/xmm"

	// "xds/xmap/entry"
	"github.com/spf13/cast"

	"github.com/heiyeluren/xds/xmap/entry"
)

type User struct {
	Name int
	Age  int
	Addr string
}

// mudi : 1001  ->  10000
// 1001 << 1 -> 10010
// 10010 & (1001 & 0)
func round(n uintptr) uintptr {
	return (n << 1) & (0 & (n))
}

type mmanClass uint8

func makemmanClass(sizeclass uint8, noscan bool) mmanClass {
	return mmanClass(sizeclass<<1) | mmanClass(bool2int(noscan))
}

func (sc mmanClass) sizeclass() int8 {
	return int8(sc >> 1)
}

func (sc mmanClass) noscan() bool {
	return sc&1 != 0
}

func bool2int(x bool) int {
	// Avoid branches. In the SSA compiler, this compiles to
	// exactly what you would want it to.
	return int(uint8(*(*uint8)(unsafe.Pointer(&x))))
}

func Init() {
	// 略
	runtime.GOMAXPROCS(6)              // 限制 CPU 使用数，避免过载
	runtime.SetMutexProfileFraction(1) // 开启对锁调用的跟踪
	runtime.SetBlockProfileRate(1)     // 开启对阻塞操作的跟踪

	go func() {
		// 启动一个 http server，注意 pprof 相关的 handler 已经自动注册过了
		if err := http.ListenAndServe(":6060", nil); err != nil {
			log.Fatal(err)
		}
		os.Exit(0)
	}()
	<-time.After(time.Second * 10)
}

func Test_xmmanPool222(t *testing.T) {
	// 同步扩容 680  异步扩容  551
	fmt.Println(100000 / (4096 / 48))
	f := &xmm.Factory{}

	mm, err := f.CreateMemory(0.6)
	if err != nil {
		t.Fatal(err)
	}
	var s unsafe.Pointer
	sl, err := mm.AllocSlice(unsafe.Sizeof(s), 100000+1, 0)
	if err != nil {
		t.Fatal(err)
	}
	ssss := *(*[]unsafe.Pointer)(sl)
	var start unsafe.Pointer
	data := (*reflect.SliceHeader)(unsafe.Pointer(&ssss)).Data
	var us []unsafe.Pointer
	for i := 0; i < 100000; i++ {
		p, err := mm.Alloc(unsafe.Sizeof(User{}))
		if err != nil {
			t.Fatal(err)
		}
		user := (*User)(p)
		user.Age = i
		user.Name = rand.Int()
		us = append(us, p)
		ssss = append(ssss, p)
		if start == nil {
			start = ssss[0]
		}
	}
	if (*reflect.SliceHeader)(unsafe.Pointer(&ssss)).Data != data {
		t.Fatal("扩容了")
	}
	fmt.Printf("sssssss %d     %d\n ", start, ssss[0])
	if ssss[0] != start {
		t.Fatal("-")
	}
	for i, pointer := range us {
		tep := (*User)(ssss[i])
		if sss := (*User)(pointer); sss.Age != i || tep.Age != sss.Age {
			t.Fatalf("%+v\n", pointer)
		}
	}
}

func TestPointer2(t *testing.T) {
	tmp := make([]*User, 10000000)
	us := &tmp
	var wait sync.WaitGroup
	wait.Add(80)
	var sm sync.Map
	for j := 0; j < 80; j++ {
		go func(z int) {
			defer wait.Done()
			for i := 0; i < 10000000; i++ {
				key := cast.ToString(i + (z * 1000))
				addr := (*unsafe.Pointer)(unsafe.Pointer(&(*us)[i]))
				user := atomic.LoadPointer(addr)
				if user == nil {
					ut := &User{
						Name: i,
						Age:  i,
						Addr: key,
					}
					ptr := unsafe.Pointer(ut)
					if atomic.CompareAndSwapPointer(addr, nil, ptr) {
						sm.Store(i, uintptr(ptr))
					}
				}
			}
		}(j)
	}
	wait.Wait()

	for i := 0; i < 10000000; i++ {
		addr := (*unsafe.Pointer)(unsafe.Pointer(&(*us)[i]))
		user := atomic.LoadPointer(addr)
		u := (*User)(user)
		if val, ok := sm.Load(i); !ok || val != uintptr(unsafe.Pointer(u)) {
			t.Fatal(val, user, i, u)
		}
	}
}

func TestPointer(t *testing.T) {
	tmp := make([]uintptr, 1000000)
	var users []*User
	us := &tmp
	var wait sync.WaitGroup
	wait.Add(80)
	var sm sync.Map
	for j := 0; j < 80; j++ {
		go func(z int) {
			defer wait.Done()
			for i := 0; i < 100000; i++ {
				key := cast.ToString(i + (z * 1000))
				addr := &((*us)[i])
				user := atomic.LoadUintptr(addr)
				if user == 0 {
					ut := &User{
						Name: i,
						Age:  i,
						Addr: key,
					}
					ptr := uintptr(unsafe.Pointer(ut))
					if atomic.CompareAndSwapUintptr(addr, 0, ptr) {
						users = append(users, ut)
						sm.Store(i, ptr)
						// fmt.Printf("i:%d ptr:%d\n", i, ptr)
					}
				}
			}
		}(j)
	}
	wait.Wait()

	for i := 0; i < 100000; i++ {
		addr := &((*us)[i])
		user := atomic.LoadUintptr(addr)
		u := (*User)(unsafe.Pointer(user))
		if val, ok := sm.Load(i); !ok || val != user {
			t.Fatal(val, user, i, u)
		}
	}
}

// todo  CompareAndSwapPointer xuexi

func TestRBTree(t *testing.T) {
	rbt := new(entry.Tree)
	rbt.SetComparator(func(o1, o2 interface{}) int {
		key1, key2 := o1.(string), o2.(string)
		return strings.Compare(key1, key2)
	})
	num := 10000000
	st := time.Now()
	for i := 0; i < num/10; i++ {
		key := cast.ToString(i)
		ce := &entry.NodeEntry{
			Value: []byte(key),
			Key:   []byte(key),
			Hash:  uint64(rand.Int()),
		}
		rbt.Put(ce)
	}
	for i := 0; i < num/10; i++ {
		exist, node := rbt.Get([]byte(strconv.Itoa(i)))
		if !exist || bytes.Compare(node, []byte(strconv.Itoa(i))) != 0 {
			panic(i)
		}
	}
	fmt.Println(rbt.Get([]byte(strconv.Itoa(5))))
	fmt.Println(time.Now().Sub(st).Seconds())
	rbt.Walk(&entry.HookVisitor{Hook: func(node *entry.NodeEntry) {
		fmt.Println(node)
	}})
}

func Test_NewDefaultConcurrentHashMap(t *testing.T) {
	f := &xmm.Factory{}
	mm, err := f.CreateMemory(0.6)
	if err != nil {
		t.Fatal(err)
	}
	chmp, err := NewDefaultConcurrentHashMap(mm, Uintptr, Uintptr)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10000000; i++ {
		s := uintptr(i)
		if err := chmp.Put(s, s); err != nil {
			t.Error(err)
		}
	}
	for i := 0; i < 10000000; i++ {
		s := uintptr(i)
		if val, exist, err := chmp.Get(s); err != nil || val != s || !exist {
			t.Error("sss", err)
		}
	}
}
