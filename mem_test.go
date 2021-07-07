package memory

import (
	"context"
	"encoding/json"
	"errors"
	"io/ioutil"
	"log"
	"runtime"
	"strconv"
	"sync"
	"testing"
)

const (
	FAILURE = 500
	SUCCESS = 200
)

type Response struct {
	Result  int    `json:"status"`
	Message string `json:"message"`
	Body    []byte `json:"body"`
}

func TestDemo(t *testing.T) {
	codebuf, _ := ioutil.ReadFile("./wasm/main.wasm")

	r, err := Start("test", codebuf)
	if err != nil {
		panic("Start Error")
	}

	// batch
	count := 30000
	wg := sync.WaitGroup{}
	wg.Add(count)
	for i := 0; i < count; i++ {
		go func(index int) {
			err := r.Invoke(index)
			if err != nil {
				println(err.Error())
			}
			wg.Done()
		}(i)
	}

	wg.Wait()

	println("done")

	r.runtime.Release()

	runtime.GC()

	select {}
}

type R struct {
	runtime Runtime
	lock    sync.Mutex
}

func Start(name string, codebuf []byte) (*R, error) {
	runtime, _ := RuntimeFromBytes("test", codebuf, nil)

	// import Call
	runtime.SetFunc("Sys_Call", func(args []interface{}) (i interface{}, e error) {
		// get params
		funcName, _ := String(args[0])
		byteArgs, _ := Bytes(args[1])

		byteData := [][]byte{}
		err := json.Unmarshal(byteArgs, &byteData)
		if err != nil {
			resByte, _ := json.Marshal(Response{
				Result:  FAILURE,
				Message: "args Unmarshal error, Error: " + err.Error(),
			})
			return FromBytes(resByte), nil
		}

		switch funcName {
		case "Logln":
			args := []interface{}{}
			json.Unmarshal(byteData[0], &args)
			log.Println(args...)
			return FromBytes(nil), nil
		default:
			log.Println(args...)
			return FromBytes(nil), nil
		}
	})

	// start
	init := make(chan error)
	ctx, cancF := context.WithCancel(context.Background())
	runtime.CancF = func() {
		runtime.ClearInstance()
		cancF()
	}

	go runtime.Run(ctx, init)

	return &R{
		runtime: *runtime,
	}, <-init
}

func (r *R) Invoke(index int) error {
	r.lock.Lock()
	defer r.lock.Unlock()

	res, err := r.runtime.CallFunc("logFunc", []interface{}{"test log " + strconv.Itoa(index)})
	if err != nil {
		return errors.New("CallFunc Error: " + err.Error())
	}
	resByte, err := Bytes(res)
	if err != nil {
		return errors.New("Response Convert Bytes Error: " + err.Error())
	}

	var response = Response{}
	json.Unmarshal(resByte, &response)

	println("invoke", index, ":", response.Result, response.Message)

	return nil
}
