package memory

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"reflect"
	"sync"
	"syscall"
	"time"

	"github.com/wasmerio/wasmer-go/wasmer"
)

var (
	undefined = &struct{}{}
)

func getRuntime(env interface{}) *Runtime {
	return env.(*Runtime)
}

type Runtime struct {
	name         string
	module       *wasmer.Module
	imp          *wasmer.ImportObject
	instance     *wasmer.Instance
	exitCode     int
	cancelStatus bool

	valueIDX int
	valueMap map[int]interface{}
	refs     map[interface{}]int
	refCount map[int]int
	idPools  []int

	valuesMu sync.RWMutex
	memory   []byte
	Exited   bool
	CancF    context.CancelFunc
}

type object struct {
	name  string // for debugging
	props map[string]interface{}
	new   func(args []interface{}) interface{}
}

type array struct {
	buf []byte
}

type Func func(args []interface{}) (interface{}, error)

type funcWrapper struct {
	id interface{}
}

func RuntimeFromBytes(name string, bytes []byte, imports *wasmer.ImportObject) (*Runtime, error) {
	b := new(Runtime)
	b.name = name

	engine := wasmer.NewEngine()
	store := wasmer.NewStore(engine)

	module, _ := wasmer.NewModule(store, bytes)

	if imports == nil {
		imports = wasmer.NewImportObject()
	}

	err := b.addImports(imports, store)
	if err != nil {
		return nil, err
	}
	b.imp = imports
	b.module = module

	// 初始版本使用instance 现已经改进为每次invoke预生成instance
	inst, err := wasmer.NewInstance(module, imports)
	if err != nil {
		return nil, err
	}
	b.instance = inst

	b.addValues()
	b.refs = make(map[interface{}]int)
	b.valueIDX = 8
	b.refCount = make(map[int]int)
	// b.idPools = []int{}

	return b, nil
}

func (b *Runtime) addValues() {
	var goObj *object
	goObj = propObject("jsGo", map[string]interface{}{
		"_makeFuncWrapper": Func(func(args []interface{}) (interface{}, error) {
			return &funcWrapper{id: args[0]}, nil
		}),
		"_pendingEvent": nil,
	})
	b.valueMap = map[int]interface{}{
		0: math.NaN(),
		1: float64(0),
		2: nil,
		3: true,
		4: false,
		5: &object{
			props: map[string]interface{}{
				"Object": &object{name: "Object", new: func(args []interface{}) interface{} {
					return &object{name: "ObjectInner", props: map[string]interface{}{}}
				}},
				"Array":      arrayObject("Array"),
				"Uint8Array": arrayObject("Uint8Array"),
				"process":    propObject("process", nil),
				"Date": &object{name: "Date", new: func(args []interface{}) interface{} {
					t := time.Now()
					return &object{name: "DateInner", props: map[string]interface{}{
						"time": t,
						"getTimezoneOffset": Func(func(args []interface{}) (interface{}, error) {
							_, offset := t.Zone()

							// make it negative and return in minutes
							offset = (offset / 60) * -1
							return offset, nil
						}),
					}}
				}},
				"crypto": propObject("crypto", map[string]interface{}{
					"getRandomValues": Func(func(args []interface{}) (interface{}, error) {
						arr := args[0].(*array)
						return rand.Read(arr.buf)
					}),
				}),
				"AbortController": &object{name: "AbortController", new: func(args []interface{}) interface{} {
					return &object{name: "AbortControllerInner", props: map[string]interface{}{
						"signal": propObject("signal", map[string]interface{}{}),
					}}
				}},
				"Headers": &object{name: "Headers", new: func(args []interface{}) interface{} {
					headers := http.Header{}
					obj := &object{name: "HeadersInner", props: map[string]interface{}{
						"headers": headers,
						"append": Func(func(args []interface{}) (interface{}, error) {
							headers.Add(args[0].(string), args[1].(string))
							return nil, nil
						}),
					}}

					return obj
				}},
				"fetch": Func(func(args []interface{}) (interface{}, error) {
					// Fixme(ved): implement fetch
					log.Fatalln(args)
					return nil, nil
				}),
				"fs": propObject("fs", map[string]interface{}{
					"constants": propObject("constants", map[string]interface{}{
						"O_WRONLY": syscall.O_WRONLY,
						"O_RDWR":   syscall.O_RDWR,
						"O_CREAT":  syscall.O_CREAT,
						"O_TRUNC":  syscall.O_TRUNC,
						"O_APPEND": syscall.O_APPEND,
						"O_EXCL":   syscall.O_EXCL,
					}),

					"write": Func(func(args []interface{}) (interface{}, error) {
						fd := int(args[0].(float64))
						offset := int(args[2].(float64))
						length := int(args[3].(float64))
						buf := args[1].(*array).buf[offset : offset+length]
						pos := args[4]
						callback := args[5].(*funcWrapper)
						var err error
						var n int
						if pos != nil {
							position := int64(pos.(float64))
							n, err = syscall.Pwrite(fd, buf, position)
						} else {
							n, err = syscall.Write(fd, buf)
						}

						if err != nil {
							return nil, err
						}

						return b.makeFuncWrapper(callback.id, goObj, &[]interface{}{nil, n})
					}),
				}),
			},
		}, // global
		6: goObj, // jsGo
	}
}

func (b *Runtime) check() error {
	if b.Exited {
		return errors.New("WASM instance already Exited")
	}

	return nil
}

func (b *Runtime) Run(ctx context.Context, init chan error) {
	if err := b.check(); err != nil {
		init <- err
		return
	}

	run, err := b.instance.Exports.GetFunction("run")
	if err != nil {
		init <- err
		return
	}

	_, err = run(0, 0)
	if err != nil {
		println(err.Error())
		init <- err
		return
	}

	// ctx, CancF := context.WithCancel(ctx)
	// b.CancF = CancF

	init <- nil
	select {
	case <-ctx.Done():
		log.Printf("stopping WASM[%s] instance...\n", b.name)
		b.Exited = true
		b.Release()
		return
	}
}

func (b *Runtime) mem(offset int32) []byte {
	m, _ := b.instance.Exports.GetMemory("mem")
	if b.memory == nil {
		b.memory = m.Data()
	}

	if len(b.memory) <= int(offset) {
		m.Grow(1)
		b.memory = m.Data()
	}

	return b.memory
}

func (b *Runtime) getSP() int32 {
	spFunc, _ := b.instance.Exports.GetFunction("getsp")
	val, err := spFunc()
	if err != nil {
		panic("failed to get sp")
	}

	return val.(int32)
}

func (b *Runtime) setUint8(offset int32, v uint8) {
	mem := b.mem(offset)
	mem[offset] = byte(v)
}

func (b *Runtime) setInt64(offset int32, v int64) {
	mem := b.mem(offset)
	binary.LittleEndian.PutUint64(mem[offset:], uint64(v))
}

func (b *Runtime) setInt32(offset int32, v int32) {
	mem := b.mem(offset)
	binary.LittleEndian.PutUint32(mem[offset:], uint32(v))
}

func (b *Runtime) getInt64(offset int32) int64 {
	mem := b.mem(offset)
	return int64(binary.LittleEndian.Uint64(mem[offset:]))
}

func (b *Runtime) getInt32(offset int32) int32 {
	mem := b.mem(offset)
	return int32(binary.LittleEndian.Uint32(mem[offset:]))
}

func (b *Runtime) setUint32(offset int32, v uint32) {
	mem := b.mem(offset)
	binary.LittleEndian.PutUint32(mem[offset:], v)
}

func (b *Runtime) setUint64(offset int32, v uint64) {
	mem := b.mem(offset)
	binary.LittleEndian.PutUint64(mem[offset:], v)
}

func (b *Runtime) getUnit64(offset int32) uint64 {
	mem := b.mem(offset)
	return binary.LittleEndian.Uint64(mem[offset+0:])
}

func (b *Runtime) setFloat64(offset int32, v float64) {
	uf := math.Float64bits(v)
	b.setUint64(offset, uf)
}

func (b *Runtime) getFloat64(offset int32) float64 {
	uf := b.getUnit64(offset)
	return math.Float64frombits(uf)
}

func (b *Runtime) getUint32(offset int32) uint32 {
	return binary.LittleEndian.Uint32(b.mem(offset)[offset+0:])
}

func (b *Runtime) loadSlice(offset int32) []byte {
	mem := b.mem(offset)
	array := binary.LittleEndian.Uint64(mem[offset+0:])
	length := binary.LittleEndian.Uint64(mem[offset+8:])
	return mem[array : array+length]
}

func (b *Runtime) loadString(addr int32) string {
	d := b.loadSlice(addr)
	return string(d)
}

func (b *Runtime) loadSliceOfValues(addr int32) []interface{} {
	arr := int(b.getInt64(addr + 0))
	arrLen := int(b.getInt64(addr + 8))
	vals := make([]interface{}, arrLen, arrLen)
	for i := 0; i < int(arrLen); i++ {
		vals[i] = b.loadValue(int32(arr + i*8))
	}

	return vals
}

func (b *Runtime) loadValue(addr int32) interface{} {
	f := b.getFloat64(addr)
	if f == 0 {
		return undefined
	}

	if !math.IsNaN(f) {
		return f
	}

	b.valuesMu.RLock()
	defer b.valuesMu.RUnlock()

	return b.valueMap[int(b.getUint32(addr))]
}

func (b *Runtime) storeValue(addr int32, v interface{}) {
	const nanHead = 0x7FF80000

	if i, ok := v.(int); ok {
		v = float64(i)
	}

	if i, ok := v.(uint); ok {
		v = float64(i)
	}

	if v, ok := v.(float64); ok {
		if math.IsNaN(v) {
			b.setUint32(addr+4, nanHead)
			b.setUint32(addr, 0)
			return
		}

		if v == 0 {
			b.setUint32(addr+4, nanHead)
			b.setUint32(addr, 1)
			return
		}

		b.setFloat64(addr, v)
		return
	}

	switch v {
	case undefined:
		b.setFloat64(addr, 0)
		return
	case nil:
		b.setUint32(addr+4, nanHead)
		b.setUint32(addr, 2)
		return
	case true:
		b.setUint32(addr+4, nanHead)
		b.setUint32(addr, 3)
		return
	case false:
		b.setUint32(addr+4, nanHead)
		b.setUint32(addr, 4)
		return
	}

	rt := reflect.TypeOf(v)
	if rt.Kind() == reflect.Ptr {
		rt = rt.Elem()
	}

	rv := v
	if !rt.Comparable() {
		rv = reflect.ValueOf(v)
	}

	ref, ok := b.refs[rv]
	b.valuesMu.RLock()
	if !ok {
		b.valueMap[b.valueIDX] = v
		ref = b.valueIDX
		b.refs[rv] = ref
		b.refCount[ref] = 0
		b.valueIDX++
	}
	b.refCount[ref]++
	b.valuesMu.RUnlock()

	typeFlag := 0

	// golang v1.13
	// switch rt.Kind() {
	// case reflect.String:
	// 	typeFlag = 1
	// case reflect.Func:
	// 	typeFlag = 3
	// }

	// golang v1.14+
	switch rt.Kind() {
	case reflect.String:
		typeFlag = 2
	case reflect.Func:
		typeFlag = 4
	// case reflect.Struct:
	// 	typeFlag = 1
	// case reflect.Slice:
	// 	typeFlag = 1
	default:
		typeFlag = 1
	}

	b.setUint32(addr+4, uint32(nanHead|typeFlag))
	b.setUint32(addr, uint32(ref))
}

func propObject(name string, prop map[string]interface{}) *object {
	return &object{name: name, props: prop}
}

func arrayObject(name string) *object {
	return &object{
		name: name,
		new: func(args []interface{}) interface{} {
			l := int(args[0].(float64))
			return &array{
				buf: make([]byte, l, l),
			}
		},
	}
}

func (b *Runtime) resume() error {
	res, _ := b.instance.Exports.GetFunction("resume")
	_, err := res()
	return err
}

func (b *Runtime) makeFuncWrapper(id, this interface{}, args *[]interface{}) (interface{}, error) {
	goObj := this.(*object)
	event := propObject("_pendingEvent", map[string]interface{}{
		"id":   id,
		"this": goObj,
		"args": args,
	})

	goObj.props["_pendingEvent"] = event
	err := b.resume()
	if err != nil {
		return nil, err
	}

	return event.props["result"], nil
}

func (b *Runtime) CallFunc(fn string, args []interface{}) (interface{}, error) {
	if err := b.check(); err != nil {
		return nil, err
	}

	b.valuesMu.RLock()

	fw, ok := b.valueMap[5].(*object).props[fn]
	if !ok {
		b.valuesMu.RUnlock()
		return nil, fmt.Errorf("missing function: %v", fn)
	}
	this := b.valueMap[6]
	b.valuesMu.RUnlock()
	return b.makeFuncWrapper(fw.(*funcWrapper).id, this, &args)
}

func (b *Runtime) SetFunc(fname string, fn Func) error {
	b.valuesMu.RLock()
	defer b.valuesMu.RUnlock()
	b.valueMap[5].(*object).props[fname] = &fn
	return nil
}

func (b *Runtime) ClearInstance() {
	b.instance = nil
	b.valueMap = nil
	b.refs = nil
	b.valueIDX = 8
	b.refCount = nil
	b.memory = nil
}

func (b *Runtime) Release() {
	if b.CancF != nil {
		b.CancF()
	}

	b.module = nil
	b.imp = nil
	b.ClearInstance()
}

func Bytes(v interface{}) ([]byte, error) {
	arr, ok := v.(*array)
	if !ok {
		return nil, fmt.Errorf("got %T instead of bytes", v)
	}

	return arr.buf, nil
}

func String(v interface{}) (string, error) {
	str, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("got %t instead of string", v)
	}

	return str, nil
}

func Error(v interface{}) (errVal, err error) {
	str, ok := v.(string)
	if !ok {
		return nil, fmt.Errorf("got %T instead of error", v)
	}

	return errors.New(str), nil
}

func FromBytes(v []byte) interface{} {
	buf := make([]byte, len(v), len(v))
	copy(buf, v)
	return &array{buf: buf}
}
