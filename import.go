package memory

import (
	"crypto/rand"
	"fmt"
	"log"
	"reflect"
	"syscall"
	"time"

	"github.com/wasmerio/wasmer-go/wasmer"
)

func debug(env interface{}, v []wasmer.Value) (out []wasmer.Value, err error) {
	sp := v[0].I32()
	log.Println(sp)
	return
}

func wexit(env interface{}, v []wasmer.Value) (out []wasmer.Value, err error) {
	b := getRuntime(env)
	sp := v[0].I32()
	b.exitCode = int(b.getUint32(sp + 8))
	println("exit code: ", b.exitCode)
	if b.CancF != nil {
		b.CancF()
	}
	return
}

func wwrite(env interface{}, v []wasmer.Value) (out []wasmer.Value, err error) {
	b := getRuntime(env)
	sp := v[0].I32()
	fd := int(b.getInt64(sp + 8))
	p := int(b.getInt64(sp + 16))
	l := int(b.getInt32(sp + 24))

	_, err = syscall.Write(fd, b.mem(int32(p))[p:p+l])
	if err != nil {
		// panic(fmt.Errorf("wasm-write: %v", err))
		log.Printf("wasm-write: %v \n", b.name)
		if b.CancF != nil {
			b.CancF()
		}
		return
	}
	return
}

func nanotime(env interface{}, v []wasmer.Value) (out []wasmer.Value, err error) {
	b := getRuntime(env)
	sp := v[0].I32()
	n := time.Now().UnixNano()
	b.setInt64(sp+8, n)
	return
}

func walltime(env interface{}, v []wasmer.Value) (out []wasmer.Value, err error) {
	b := getRuntime(env)
	sp := v[0].I32()
	t := time.Now().UnixNano()
	nanos := t % int64(time.Second)
	b.setInt64(sp+8, t/int64(time.Second))
	b.setInt32(sp+16, int32(nanos))

	return
}

func scheduleCallback(env interface{}, v []wasmer.Value) (out []wasmer.Value, err error) {
	log.Println("schedule callback")
	return
}

func clearScheduledCallback(env interface{}, v []wasmer.Value) (out []wasmer.Value, err error) {
	log.Println("clear scheduled callback")
	return
}

func getRandomData(env interface{}, v []wasmer.Value) (out []wasmer.Value, err error) {
	b := getRuntime(env)
	sp := v[0].I32()
	s := b.loadSlice(sp + 8)
	_, err = rand.Read(s)
	if err != nil {
		log.Println("failed: getRandomData")
		if b.CancF != nil {
			b.CancF()
		}
		return
	}
	return
}

func stringVal(env interface{}, v []wasmer.Value) (out []wasmer.Value, err error) {
	b := getRuntime(env)
	sp := v[0].I32()
	str := b.loadString(sp + 8)
	b.storeValue(sp+24, str)
	return
}

func valueGet(env interface{}, v []wasmer.Value) (out []wasmer.Value, err error) {
	b := getRuntime(env)
	sp := v[0].I32()
	str := b.loadString(sp + 16)
	val := b.loadValue(sp + 8)
	sp = b.getSP()
	obj, ok := val.(*object)
	if !ok {
		b.storeValue(sp+32, val)
		return
	}
	res, ok := obj.props[str]
	if !ok {
		// panic(fmt.Sprintln("missing property", str, val))
		log.Println("missing property", str, val)
		if b.CancF != nil {
			b.CancF()
		}
		return
	}
	b.storeValue(sp+32, res)
	return
}

func valueSet(env interface{}, v []wasmer.Value) (out []wasmer.Value, err error) {
	b := getRuntime(env)
	sp := v[0].I32()
	val := b.loadValue(sp + 8)
	obj := val.(*object)
	prop := b.loadString(sp + 16)
	propVal := b.loadValue(sp + 32)
	obj.props[prop] = propVal
	return
}

func valueDelete(env interface{}, v []wasmer.Value) (out []wasmer.Value, err error) {
	sp := v[0].I32()
	b := getRuntime(env)
	val := b.loadValue(sp + 8)
	obj := val.(*object)
	prop := b.loadString(sp + 16)
	delete(obj.props, prop)
	return
}

func valueIndex(env interface{}, v []wasmer.Value) (out []wasmer.Value, err error) {
	b := getRuntime(env)
	sp := v[0].I32()
	l := b.loadValue(sp + 8)
	i := b.getInt64(sp + 16)
	rv := reflect.ValueOf(l)
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}

	iv := rv.Index(int(i))
	b.storeValue(sp+24, iv.Interface())
	return
}

func valueSetIndex(env interface{}, v []wasmer.Value) (out []wasmer.Value, err error) {
	log.Println("valueSetIndex")
	return
}

func valueCall(env interface{}, vals []wasmer.Value) (out []wasmer.Value, err error) {
	b := getRuntime(env)
	sp := vals[0].I32()
	v := b.loadValue(sp + 8)
	str := b.loadString(sp + 16)
	args := b.loadSliceOfValues(sp + 32)
	f, ok := v.(*object).props[str].(Func)
	if !ok {
		log.Printf("valueCall: prop not found in %v, %s \n", v.(*object).name, str)
		if b.CancF != nil {
			b.CancF()
		}
		return
	}
	sp = b.getSP()
	res, err := f(args)
	if err != nil {
		b.storeValue(sp+56, err.Error())
		b.setUint8(sp+64, 0)
		return
	}

	b.storeValue(sp+56, res)
	b.setUint8(sp+64, 1)
	return
}

func valueInvoke(env interface{}, v []wasmer.Value) (out []wasmer.Value, err error) {
	b := getRuntime(env)
	sp := v[0].I32()
	val := *(b.loadValue(sp + 8).(*Func))
	args := b.loadSliceOfValues(sp + 16)
	res, err := val(args)
	sp = b.getSP()
	if err != nil {
		b.storeValue(sp+40, err)
		b.setUint8(sp+48, 0)
		return
	}

	b.storeValue(sp+40, res)
	b.setUint8(sp+48, 1)
	return
}

func valueNew(env interface{}, v []wasmer.Value) (out []wasmer.Value, err error) {
	b := getRuntime(env)
	sp := v[0].I32()
	val := b.loadValue(sp + 8)
	args := b.loadSliceOfValues(sp + 16)
	res := val.(*object).new(args)
	sp = b.getSP()
	b.storeValue(sp+40, res)
	b.setUint8(sp+48, 1)
	return
}

func valueLength(env interface{}, v []wasmer.Value) (out []wasmer.Value, err error) {
	b := getRuntime(env)
	sp := v[0].I32()
	val := b.loadValue(sp + 8)
	rv := reflect.ValueOf(val)
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	var l int
	switch {
	case rv.Kind() == reflect.Slice:
		l = rv.Len()
	case rv.Type() == reflect.TypeOf(array{}):
		l = len(val.(*array).buf)
	default:
		// panic(fmt.Sprintf("valueLength on %T", val))
		log.Printf("valueLength on %T", val)
		if b.CancF != nil {
			b.CancF()
		}
		return
	}

	b.setInt64(sp+16, int64(l))
	return
}

func valuePrepareString(env interface{}, v []wasmer.Value) (out []wasmer.Value, err error) {
	b := getRuntime(env)
	sp := v[0].I32()
	val := b.loadValue(sp + 8)
	var str string
	if val != nil {
		str = fmt.Sprint(val)
	}

	b.storeValue(sp+16, str)
	b.setInt64(sp+24, int64(len(str)))
	return
}

func valueLoadString(env interface{}, v []wasmer.Value) (out []wasmer.Value, err error) {
	b := getRuntime(env)
	sp := v[0].I32()
	str := b.loadValue(sp + 8).(string)
	sl := b.loadSlice(sp + 16)
	copy(sl, str)
	return
}

func valueInstanceOf(env interface{}, v []wasmer.Value) (out []wasmer.Value, err error) {
	log.Println("valueInstanceOf")
	return
}

func scheduleTimeoutEvent(env interface{}, v []wasmer.Value) (out []wasmer.Value, err error) {
	log.Println("scheduleTimeoutEvent")
	return
}

func clearTimeoutEvent(env interface{}, v []wasmer.Value) (out []wasmer.Value, err error) {
	log.Println("clearTimeoutEvent")
	return
}

func copyBytesToJS(env interface{}, v []wasmer.Value) (out []wasmer.Value, err error) {
	b := getRuntime(env)
	sp := v[0].I32()
	dst, ok := b.loadValue(sp + 8).(*array)
	if !ok {
		b.setUint8(sp+48, 0)
		return
	}
	src := b.loadSlice(sp + 16)
	n := copy(dst.buf, src[:len(dst.buf)])
	b.setInt64(sp+40, int64(n))
	b.setUint8(sp+48, 1)
	return
}

func copyBytesToGo(env interface{}, v []wasmer.Value) (out []wasmer.Value, err error) {
	b := getRuntime(env)
	sp := v[0].I32()
	dst := b.loadSlice(sp + 8)
	src, ok := b.loadValue(sp + 32).(*array)
	if !ok {
		b.setUint8(sp+48, 0)
		return
	}
	n := copy(dst, src.buf[:len(dst)])
	b.setInt64(sp+40, int64(n))
	b.setUint8(sp+48, 1)
	return
}

func resetMemoryDataView(env interface{}, v []wasmer.Value) (out []wasmer.Value, err error) {
	log.Println("resetMemoryDataView")
	b := getRuntime(env)
	m, _ := b.instance.Exports.GetMemory("mem")
	b.memory = m.Data()
	return
}

func finalizeRef(env interface{}, v []wasmer.Value) (out []wasmer.Value, err error) {
	sp := v[0].I32()

	b := getRuntime(env)
	// log.Println("finalizeRef: ", len(b.valueMap), ", refs: ", len(b.refs), ", refcout: ", len(b.refCount))
	id := int(b.getUint32(sp + 8))

	b.valuesMu.RLock()
	b.refCount[id]--
	if b.refCount[id] == 0 {
		v, ok := b.valueMap[id]

		if ok {
			rt := reflect.TypeOf(v)
			if rt.Kind() == reflect.Ptr {
				rt = rt.Elem()
			}
			rv := v
			if !rt.Comparable() {
				rv = reflect.ValueOf(v)
			}

			delete(b.refs, rv)
			delete(b.valueMap, id)
			delete(b.refCount, id)
			// b.idPools = append(b.idPools, id)
		}
	}
	b.valuesMu.RUnlock()

	return
}

// addImports adds go Runtime imports in "go" namespace.
func (b *Runtime) addImports(imps *wasmer.ImportObject, store *wasmer.Store) error {
	fnMap := make(map[string]wasmer.IntoExtern)
	ft := wasmer.NewFunctionType(wasmer.NewValueTypes(wasmer.I32), wasmer.NewValueTypes())
	fnMap["debug"] = wasmer.NewFunctionWithEnvironment(store, ft, b, debug)
	fnMap["runtime.resetMemoryDataView"] = wasmer.NewFunctionWithEnvironment(store, ft, b, resetMemoryDataView)
	fnMap["runtime.wasmExit"] = wasmer.NewFunctionWithEnvironment(store, ft, b, wexit)
	fnMap["runtime.wasmWrite"] = wasmer.NewFunctionWithEnvironment(store, ft, b, wwrite)
	fnMap["runtime.nanotime"] = wasmer.NewFunctionWithEnvironment(store, ft, b, nanotime)
	fnMap["runtime.nanotime1"] = wasmer.NewFunctionWithEnvironment(store, ft, b, nanotime)
	fnMap["runtime.walltime"] = wasmer.NewFunctionWithEnvironment(store, ft, b, walltime)
	fnMap["runtime.walltime1"] = wasmer.NewFunctionWithEnvironment(store, ft, b, walltime)
	fnMap["runtime.scheduleCallback"] = wasmer.NewFunctionWithEnvironment(store, ft, b, scheduleCallback)
	fnMap["runtime.clearScheduledCallback"] = wasmer.NewFunctionWithEnvironment(store, ft, b, clearScheduledCallback)
	fnMap["runtime.getRandomData"] = wasmer.NewFunctionWithEnvironment(store, ft, b, getRandomData)
	fnMap["runtime.scheduleTimeoutEvent"] = wasmer.NewFunctionWithEnvironment(store, ft, b, scheduleTimeoutEvent)
	fnMap["runtime.clearTimeoutEvent"] = wasmer.NewFunctionWithEnvironment(store, ft, b, clearTimeoutEvent)
	fnMap["syscall/js.stringVal"] = wasmer.NewFunctionWithEnvironment(store, ft, b, stringVal)
	fnMap["syscall/js.valueGet"] = wasmer.NewFunctionWithEnvironment(store, ft, b, valueGet)
	fnMap["syscall/js.valueSet"] = wasmer.NewFunctionWithEnvironment(store, ft, b, valueSet)
	fnMap["syscall/js.valueDelete"] = wasmer.NewFunctionWithEnvironment(store, ft, b, valueDelete)
	fnMap["syscall/js.valueIndex"] = wasmer.NewFunctionWithEnvironment(store, ft, b, valueIndex)
	fnMap["syscall/js.valueSetIndex"] = wasmer.NewFunctionWithEnvironment(store, ft, b, valueSetIndex)
	fnMap["syscall/js.valueCall"] = wasmer.NewFunctionWithEnvironment(store, ft, b, valueCall)
	fnMap["syscall/js.valueInvoke"] = wasmer.NewFunctionWithEnvironment(store, ft, b, valueInvoke)
	fnMap["syscall/js.valueNew"] = wasmer.NewFunctionWithEnvironment(store, ft, b, valueNew)
	fnMap["syscall/js.valueLength"] = wasmer.NewFunctionWithEnvironment(store, ft, b, valueLength)
	fnMap["syscall/js.valuePrepareString"] = wasmer.NewFunctionWithEnvironment(store, ft, b, valuePrepareString)
	fnMap["syscall/js.valueInstanceOf"] = wasmer.NewFunctionWithEnvironment(store, ft, b, valueInstanceOf)
	fnMap["syscall/js.valueLoadString"] = wasmer.NewFunctionWithEnvironment(store, ft, b, valueLoadString)
	fnMap["syscall/js.copyBytesToGo"] = wasmer.NewFunctionWithEnvironment(store, ft, b, copyBytesToGo)
	fnMap["syscall/js.copyBytesToJS"] = wasmer.NewFunctionWithEnvironment(store, ft, b, copyBytesToJS)
	fnMap["syscall/js.finalizeRef"] = wasmer.NewFunctionWithEnvironment(store, ft, b, finalizeRef)

	imps.Register("go", fnMap)

	return nil
}
