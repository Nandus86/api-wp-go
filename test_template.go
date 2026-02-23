package main

import (
	"fmt"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

func main() {
	msg := &waE2E.Message{
		ViewOnceMessage: &waE2E.FutureProofMessage{
			Message: &waE2E.Message{
				TemplateMessage: &waE2E.TemplateMessage{
					HydratedTemplate: &waE2E.TemplateMessage_HydratedFourRowTemplate{
						TemplateId:          proto.String("id_0"),
						HydratedTitleText:   proto.String("title"),
						HydratedContentText: proto.String("text"),
						HydratedFooterText:  proto.String("footer"),
						HydratedButtons: []*waE2E.HydratedTemplateButton{
							{
								Index: proto.Uint32(1),
								HydratedButton: &waE2E.HydratedTemplateButton_QuickReplyButton{
									QuickReplyButton: &waE2E.HydratedTemplateButton_HydratedQuickReplyButton{
										DisplayText: proto.String("Btn 1"),
										Id:          proto.String("id_1"),
									},
								},
							},
						},
					},
				},
			},
		},
	}
	fmt.Printf("%+v\n", msg)
}
