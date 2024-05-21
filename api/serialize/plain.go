package serialize

import (
	plainsdk "github.com/team-plain/go-sdk"
	"github.com/team-plain/go-sdk/customercards"

	"github.com/volatiletech/null/v8"
)

type PlainCustomerCardRow struct {
	UserID          string `json:"user_id"`
	ApplicationName string `json:"application_name"`
	ApplicationID   string `json:"application_id"`
	InstanceID      string `json:"instance_id"`
	InstanceEnv     string `json:"instance_env"`
	PlanTitle       string `json:"plan_title"`
}

func PlainCustomerCard(key string, rows []PlainCustomerCardRow) *customercards.Response {
	// For how to QA changes to this output,
	// see https://app.plain.com/developer/customer-cards-playground
	components := []plainsdk.Component{
		// return 1 component so Plain doesn't error if the DB returns no rows
		{
			ComponentSpacer: &plainsdk.ComponentSpacer{
				SpacerSize: plainsdk.ComponentSpacerSizeXS,
			},
		},
	}

	// for each row, create our Plain components
	for _, row := range rows {
		textcolorMuted := plainsdk.ComponentTextColorMuted
		textSizeS := plainsdk.ComponentTextSizeS

		components = append(components, plainsdk.Component{
			ComponentContainer: &plainsdk.ComponentContainer{
				ContainerContent: []plainsdk.ComponentContainerContentUnionInput{
					{
						ComponentSpacer: &plainsdk.ComponentSpacer{
							SpacerSize: plainsdk.ComponentSpacerSizeXS,
						},
					},
					//  app name  [Badge]
					{
						ComponentRow: &plainsdk.ComponentRow{
							RowMainContent: []plainsdk.ComponentRowContentUnionInput{
								{
									ComponentText: &plainsdk.ComponentText{
										Text:      row.ApplicationName,
										TextColor: &textcolorMuted,
									},
								},
							},
							RowAsideContent: []plainsdk.ComponentRowContentUnionInput{
								{
									ComponentBadge: &plainsdk.ComponentBadge{
										BadgeLabel: row.InstanceEnv,
										BadgeColor: envToBadgeColor(row.InstanceEnv),
									},
								},
							},
						},
					},
					{
						ComponentSpacer: &plainsdk.ComponentSpacer{
							SpacerSize: plainsdk.ComponentSpacerSizeM,
						},
					},
					// Application ID
					{
						ComponentText: &plainsdk.ComponentText{
							Text:      "Application ID",
							TextSize:  &textSizeS,
							TextColor: &textcolorMuted,
						},
					},
					// {application_id}       copy
					{
						ComponentRow: &plainsdk.ComponentRow{
							RowMainContent: []plainsdk.ComponentRowContentUnionInput{
								{
									ComponentText: &plainsdk.ComponentText{
										Text:     row.ApplicationID,
										TextSize: &textSizeS,
									},
								},
							},
							RowAsideContent: []plainsdk.ComponentRowContentUnionInput{
								{
									ComponentCopyButton: &plainsdk.ComponentCopyButton{
										CopyButtonValue:        row.ApplicationID,
										CopyButtonTooltipLabel: null.StringFrom("Copy Application Id").Ptr(),
									},
								},
							},
						},
					},
					{
						ComponentSpacer: &plainsdk.ComponentSpacer{
							SpacerSize: plainsdk.ComponentSpacerSizeM,
						},
					},
					// Instance ID
					{
						ComponentText: &plainsdk.ComponentText{
							Text:      "Instance ID",
							TextSize:  &textSizeS,
							TextColor: &textcolorMuted,
						},
					},
					// {instance_id}       copy
					{
						ComponentRow: &plainsdk.ComponentRow{
							RowMainContent: []plainsdk.ComponentRowContentUnionInput{
								{
									ComponentText: &plainsdk.ComponentText{
										Text:     row.InstanceID,
										TextSize: &textSizeS,
									},
								},
							},
							RowAsideContent: []plainsdk.ComponentRowContentUnionInput{
								{
									ComponentCopyButton: &plainsdk.ComponentCopyButton{
										CopyButtonValue:        row.InstanceID,
										CopyButtonTooltipLabel: null.StringFrom("Copy Instance Id").Ptr(),
									},
								},
							},
						},
					},
					{
						ComponentSpacer: &plainsdk.ComponentSpacer{
							SpacerSize: plainsdk.ComponentSpacerSizeM,
						},
					},
					// Plan
					{
						ComponentText: &plainsdk.ComponentText{
							Text:      "Plan",
							TextSize:  &textSizeS,
							TextColor: &textcolorMuted,
						},
					},
					// {plan_title}
					{
						ComponentText: &plainsdk.ComponentText{
							Text:     row.PlanTitle,
							TextSize: &textSizeS,
						},
					},
				},
			},
		})

		components = append(components, plainsdk.Component{
			ComponentSpacer: &plainsdk.ComponentSpacer{
				SpacerSize: plainsdk.ComponentSpacerSizeM,
			},
		})
	}

	return &customercards.Response{
		Cards: []plainsdk.CustomerCard{
			{
				Key:        key,
				Components: components,
			},
		},
	}
}

// convert an instance environment to a badge color
func envToBadgeColor(env string) *plainsdk.ComponentBadgeColor {
	red := plainsdk.ComponentBadgeColorRed
	yellow := plainsdk.ComponentBadgeColorYellow
	blue := plainsdk.ComponentBadgeColorBlue
	grey := plainsdk.ComponentBadgeColorGrey
	switch env {
	case "production":
		return &red
	case "staging":
		return &yellow
	case "development":
		return &blue
	default:
		return &grey
	}
}
