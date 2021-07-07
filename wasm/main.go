package main

import (
	"encoding/json"
	"syscall/js"
)

type Response struct {
	Result  int    `json:"status"`
	Message string `json:"message"`
	Body    []byte `json:"body"`
}

func js2bytes(value interface{}) []byte {
	v := value.(js.Value)
	buf := make([]byte, v.Length(), v.Length())
	js.CopyBytesToGo(buf, v)

	return buf
}

func bytes2js(b []byte) js.Value {
	v := js.Global().Get("Uint8Array").New(len(b))
	js.CopyBytesToJS(v, b)

	return v
}

func invoke(name string, args ...[]byte) interface{} {
	byteArgs, _ := json.Marshal(args)
	jsArgs := bytes2js(byteArgs)

	// call import function
	return js.Global().Get("Sys_Call").Invoke(name, jsArgs)
}

func logFunc(args []string) Response {
	texts := []string{"in wasm log, ", args[0]}
	textByte, _ := json.Marshal(texts)

	invoke("Logln", textByte)

	return Response{200, "log ok", nil}
}

func main() {
	ch := make(chan bool)

	// export function log
	js.Global().Set("logFunc", js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		argsStr := []string{}

		for _, v := range args {
			argsStr = append(argsStr, v.String())
		}

		res := logFunc(argsStr)
		resByte, _ := json.Marshal(res)

		rv := js.Global().Get("Uint8Array").New(len(resByte))
		js.CopyBytesToJS(rv, resByte)

		return rv
	}))

	<-ch
}
