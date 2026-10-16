<?xml version="1.0" encoding="UTF-8"?>
<StyledLayerDescriptor version="1.0.0"
  xmlns="http://www.opengis.net/sld" xmlns:ogc="http://www.opengis.net/ogc"
  xmlns:xlink="http://www.w3.org/1999/xlink" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
  xsi:schemaLocation="http://www.opengis.net/sld http://schemas.opengis.net/sld/1.0.0/StyledLayerDescriptor.xsd">
  <NamedLayer>
    <Name>entities</Name>
    <UserStyle>
      <Title>Sentinel live entities</Title>
      <Abstract>Point symbol per entity type; stale entities are hollow.</Abstract>
      <FeatureTypeStyle>
        <Rule>
          <Name>aircraft</Name>
          <ogc:Filter><ogc:PropertyIsEqualTo><ogc:PropertyName>entity_type</ogc:PropertyName><ogc:Literal>AIRCRAFT</ogc:Literal></ogc:PropertyIsEqualTo></ogc:Filter>
          <PointSymbolizer>
            <Graphic>
              <Mark><WellKnownName>triangle</WellKnownName><Fill><CssParameter name="fill">#1e88e5</CssParameter></Fill><Stroke><CssParameter name="stroke">#0d47a1</CssParameter><CssParameter name="stroke-width">1</CssParameter></Stroke></Mark>
              <Size>10</Size>
              <Rotation><ogc:PropertyName>heading</ogc:PropertyName></Rotation>
            </Graphic>
          </PointSymbolizer>
        </Rule>
        <Rule>
          <Name>vessel</Name>
          <ogc:Filter><ogc:PropertyIsEqualTo><ogc:PropertyName>entity_type</ogc:PropertyName><ogc:Literal>VESSEL</ogc:Literal></ogc:PropertyIsEqualTo></ogc:Filter>
          <PointSymbolizer>
            <Graphic>
              <Mark><WellKnownName>square</WellKnownName><Fill><CssParameter name="fill">#00897b</CssParameter></Fill><Stroke><CssParameter name="stroke">#004d40</CssParameter><CssParameter name="stroke-width">1</CssParameter></Stroke></Mark>
              <Size>8</Size>
            </Graphic>
          </PointSymbolizer>
        </Rule>
        <Rule>
          <Name>satellite</Name>
          <ogc:Filter><ogc:PropertyIsEqualTo><ogc:PropertyName>entity_type</ogc:PropertyName><ogc:Literal>SATELLITE</ogc:Literal></ogc:PropertyIsEqualTo></ogc:Filter>
          <PointSymbolizer>
            <Graphic>
              <Mark><WellKnownName>star</WellKnownName><Fill><CssParameter name="fill">#8e24aa</CssParameter></Fill></Mark>
              <Size>9</Size>
            </Graphic>
          </PointSymbolizer>
        </Rule>
        <Rule>
          <Name>other</Name>
          <ElseFilter/>
          <PointSymbolizer>
            <Graphic>
              <Mark><WellKnownName>circle</WellKnownName><Fill><CssParameter name="fill">#fb8c00</CssParameter></Fill><Stroke><CssParameter name="stroke">#e65100</CssParameter><CssParameter name="stroke-width">1</CssParameter></Stroke></Mark>
              <Size>7</Size>
            </Graphic>
          </PointSymbolizer>
        </Rule>
        <Rule>
          <Name>label</Name>
          <MaxScaleDenominator>2000000</MaxScaleDenominator>
          <TextSymbolizer>
            <Label><ogc:PropertyName>name</ogc:PropertyName></Label>
            <Font><CssParameter name="font-family">Arial</CssParameter><CssParameter name="font-size">10</CssParameter></Font>
            <LabelPlacement><PointPlacement><Displacement><DisplacementX>8</DisplacementX><DisplacementY>0</DisplacementY></Displacement></PointPlacement></LabelPlacement>
            <Halo><Radius>1.5</Radius><Fill><CssParameter name="fill">#ffffff</CssParameter></Fill></Halo>
            <Fill><CssParameter name="fill">#222222</CssParameter></Fill>
          </TextSymbolizer>
        </Rule>
      </FeatureTypeStyle>
    </UserStyle>
  </NamedLayer>
</StyledLayerDescriptor>
