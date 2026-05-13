package whatsapp

import (
	"reflect"
	"sync"
    "github.com/user/whatsmeow-basileia/pkg/logger"
    "go.uber.org/zap"
)

type EventDispatcher struct {
	handlers map[reflect.Type][]func(interface{})
	mu       sync.RWMutex
}

func NewEventDispatcher() *EventDispatcher {
	return &EventDispatcher{
		handlers: make(map[reflect.Type][]func(interface{})),
	}
}

func (d *EventDispatcher) Register(eventType interface{}, handler func(interface{})) {
	d.mu.Lock()
	defer d.mu.Unlock()

	t := reflect.TypeOf(eventType)
    // If passing a pointer nil, get the element type
    if t.Kind() == reflect.Ptr {
        t = t.Elem()
    }

	d.handlers[t] = append(d.handlers[t], handler)
}

func (d *EventDispatcher) Dispatch(evt interface{}) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	t := reflect.TypeOf(evt)
    if t.Kind() == reflect.Ptr {
        t = t.Elem()
    }

	if handlers, ok := d.handlers[t]; ok {
		for _, handler := range handlers {
			go func(h func(interface{})) {
				defer func() {
					if r := recover(); r != nil {
                        logger.Error("Panic in event handler", zap.Any("recover", r))
					}
				}()
				h(evt)
			}(handler)
		}
	}
}
