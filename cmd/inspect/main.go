package main

import (
    "fmt"
    "reflect"

    "go.mau.fi/whatsmeow/proto/waE2E"
)

func main() {
    fmt.Println("=== TemplateMessage Fields ===")
   t := reflect.TypeOf(waE2E.TemplateMessage{})
    for i := 0; i < t.NumField(); i++ {
        f := t.Field(i)
        if !f.IsExported() {
            continue
        }
        fmt.Printf("  %s: %s\n", f.Name, f.Type.String())
    }
    
    fmt.Println("\n=== HydratedTemplateButton Fields ===")
    t2 := reflect.TypeOf(waE2E.HydratedTemplateButton{})
    for i := 0; i < t2.NumField(); i++ {
        f := t2.Field(i)
        if !f.IsExported() {
            continue
        }
        fmt.Printf("  %s: %s\n", f.Name, f.Type.String())
    }
}
