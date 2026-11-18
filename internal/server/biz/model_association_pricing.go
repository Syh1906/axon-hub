package biz

import (
	"context"

	"github.com/samber/lo"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channelmodelprice"
	"github.com/looplj/axonhub/internal/ent/model"
)

func (svc *ModelService) previewAssociationPrices(ctx context.Context, connections []*ModelChannelConnection, entranceID int) error {
	if len(connections) == 0 {
		return nil
	}
	db := svc.entFromContext(ctx)
	entrance, err := db.Model.Get(ctx, entranceID)
	if err != nil {
		return err
	}
	channelSet := make(map[int]bool)
	for _, conn := range connections {
		if len(conn.Models) > 0 {
			channelSet[conn.Channel.ID] = true
		}
	}
	if len(channelSet) == 0 {
		return nil
	}
	// Load model IDs rather than full price schedules. An explicit zero is an override.
	rows, err := db.ChannelModelPrice.Query().Where(
		channelmodelprice.ChannelIDIn(lo.Keys(channelSet)...), channelmodelprice.DeletedAtEQ(0),
	).Select(channelmodelprice.FieldChannelID, channelmodelprice.FieldModelID).All(ctx)
	if err != nil {
		return err
	}
	overrides := make(map[ChannelModelKey]bool, len(rows))
	for _, row := range rows {
		overrides[ChannelModelKey{row.ChannelID, row.ModelID}] = true
	}
	var lookup []string
	for _, conn := range connections {
		for _, entry := range conn.Models {
			if entry.UseActualModelPrice && !overrides[ChannelModelKey{conn.Channel.ID, entry.ActualModel}] {
				lookup = append(lookup, entry.ActualModel)
			}
		}
	}
	matches, err := matchModelStandardPrices(ctx, db, lookup, model.StatusEnabled)
	if err != nil {
		return err
	}
	cards := make(map[string]*ent.Model, len(matches))
	for _, match := range matches {
		cards[match.RequestModelID] = match.Model
	}
	for _, conn := range connections {
		for i := range conn.Models {
			entry := &conn.Models[i]
			if overrides[ChannelModelKey{conn.Channel.ID, entry.ActualModel}] {
				entry.PreviewPriceSource = "channel"
				entry.PreviewPriceModel = entry.ActualModel
				continue
			}
			card := entrance
			if entry.UseActualModelPrice {
				card = cards[entry.ActualModel]
			}
			entry.PreviewPriceSource = "unpriced"
			if card != nil {
				if _, ok := modelCardToChannelModelPrice(card.ModelCard); ok {
					entry.PreviewPriceSource = "standard"
					entry.PreviewPriceModel = card.Name
					entry.previewPriceCardID = card.ID
				}
			}
		}
	}
	return nil
}

// The management preview has a model ID but no request conditions or protocol.
// Preserve the first row for each alias and summarize reachable prices across
// all aliases for the same channel and actual model.
func mergeAssociationPricePreviews(matches []*AssociationMatch) []*ModelChannelConnection {
	type priceSource struct {
		source, model string
		cardID        int
	}
	sources := make(map[ChannelModelKey]priceSource)
	type route struct {
		channelID     int
		alias, actual string
	}
	complete := make(map[route]bool)
	var connections []*ModelChannelConnection
	for _, match := range matches {
		when := match.Association.When
		unconditional := when == nil || !when.Enabled || when.Condition == nil
		for _, conn := range match.Connections {
			models := conn.Models[:0]
			for _, entry := range conn.Models {
				alias := route{conn.Channel.ID, entry.RequestModel, entry.ActualModel}
				if done, seen := complete[alias]; done {
					continue
				} else if !seen {
					models = append(models, entry)
				}
				complete[alias] = unconditional
				actual := ChannelModelKey{conn.Channel.ID, entry.ActualModel}
				source := priceSource{entry.PreviewPriceSource, entry.PreviewPriceModel, entry.previewPriceCardID}
				if prior, seen := sources[actual]; seen && prior != source {
					if prior.source == "unpriced" || prior.source == "conditionalUnpriced" || source.source == "unpriced" {
						source = priceSource{source: "conditionalUnpriced"}
					} else {
						source = priceSource{source: "conditional"}
					}
				}
				sources[actual] = source
			}
			if len(models) != 0 || len(conn.Models) == 0 {
				conn.Models = models
				connections = append(connections, conn)
			}
		}
	}
	for _, conn := range connections {
		for i := range conn.Models {
			entry := &conn.Models[i]
			source := sources[ChannelModelKey{conn.Channel.ID, entry.ActualModel}]
			entry.PreviewPriceSource, entry.PreviewPriceModel = source.source, source.model
		}
	}
	return connections
}
