package main
import (
	"exchange/ws"
)
func main(){
	go ws.CreateServer()
	select {} // block forever
}
