package schedulers

import (
	"fmt"

	"github.com/fastschema/qjs"
)

const schedulerSchemaBuilderScript = `
(function () {
  function schema(json, validate) {
    return {
      toJSONSchema: function () { return json; },
      parse: function (value) {
        validate(value);
        return value;
      }
    };
  }
  function schemaToJSON(value) {
    if (value && typeof value.toJSONSchema === "function") {
      return value.toJSONSchema();
    }
    return value;
  }
  scheduler.z = {
    string: function () {
      return schema({ type: "string" }, function (value) {
        if (typeof value !== "string") throw new Error("expected string");
      });
    },
    number: function () {
      return schema({ type: "number" }, function (value) {
        if (typeof value !== "number") throw new Error("expected number");
      });
    },
    boolean: function () {
      return schema({ type: "boolean" }, function (value) {
        if (typeof value !== "boolean") throw new Error("expected boolean");
      });
    },
    enum: function (values) {
      return schema({ type: "string", enum: values.slice() }, function (value) {
        if (values.indexOf(value) === -1) throw new Error("expected one of " + values.join(", "));
      });
    },
    array: function (item) {
      return schema({ type: "array", items: schemaToJSON(item) }, function (value) {
        if (!Array.isArray(value)) throw new Error("expected array");
        if (item && typeof item.parse === "function") {
          for (const entry of value) item.parse(entry);
        }
      });
    },
    object: function (shape) {
      const properties = {};
      const required = [];
      for (const key of Object.keys(shape || {})) {
        properties[key] = schemaToJSON(shape[key]);
        required.push(key);
      }
      return schema({ type: "object", properties, required, additionalProperties: false }, function (value) {
        if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("expected object");
        for (const key of required) {
          if (!(key in value)) throw new Error("missing required property " + key);
          if (shape[key] && typeof shape[key].parse === "function") shape[key].parse(value[key]);
        }
        for (const key of Object.keys(value)) {
          if (!(key in properties)) throw new Error("unexpected property " + key);
        }
      });
    }
  };
})();
`

func installSchedulerSchemaBuilder(jsctx *qjs.Context) error {
	value, err := jsctx.Eval("scheduler-z.js", qjs.Code(schedulerSchemaBuilderScript))
	if err != nil {
		return fmt.Errorf("install scheduler.z schema builder: %w", err)
	}
	if value != nil {
		value.Free()
	}
	return nil
}

func parseSchedulerOutputSchema(jsctx *qjs.Context, encoder *jsValueEncoder, args []*qjs.Value, apiName string) (string, *qjs.Value, error) {
	if len(args) < 2 || args[1] == nil || args[1].IsUndefined() || args[1].IsNull() {
		return "", nil, nil
	}
	if !args[1].IsObject() || args[1].IsArray() {
		return "", nil, nil
	}
	options := args[1]
	for _, key := range []string{"outputSchema", "schema"} {
		schemaValue := options.GetPropertyStr(key)
		if schemaValue == nil || schemaValue.IsUndefined() || schemaValue.IsNull() {
			continue
		}
		schemaJSON, err := schedulerOutputSchemaJSON(jsctx, encoder, schemaValue)
		if err != nil {
			return "", nil, fmt.Errorf("decode %s %s: %w", apiName, key, err)
		}
		return schemaJSON, schemaValue, nil
	}
	return "", nil, nil
}

func schedulerOutputSchemaJSON(jsctx *qjs.Context, encoder *jsValueEncoder, value *qjs.Value) (string, error) {
	if !value.IsObject() || value.IsArray() {
		return "", fmt.Errorf("must be an object")
	}
	toJSONSchema := value.GetPropertyStr("toJSONSchema")
	if toJSONSchema != nil && toJSONSchema.IsFunction() {
		converted, err := jsctx.Invoke(toJSONSchema, value)
		if err != nil {
			return "", err
		}
		if converted == nil || converted.IsUndefined() || converted.IsNull() || !converted.IsObject() || converted.IsArray() {
			return "", fmt.Errorf("toJSONSchema must return an object")
		}
		return encoder.Encode(converted)
	}
	return encoder.Encode(value)
}

func validateSchedulerJSONWithSchema(jsctx *qjs.Context, schemaValue, responseValue *qjs.Value, apiName string) error {
	if schemaValue == nil || responseValue == nil || !schemaValue.IsObject() {
		return nil
	}
	parseFn := schemaValue.GetPropertyStr("parse")
	if parseFn == nil || !parseFn.IsFunction() {
		return nil
	}
	jsonValue := responseValue.GetPropertyStr("json")
	if jsonValue == nil || jsonValue.IsUndefined() || jsonValue.IsNull() {
		return nil
	}
	if _, err := jsctx.Invoke(parseFn, schemaValue, jsonValue); err != nil {
		return fmt.Errorf("%s JSON output does not match outputSchema: %w", apiName, err)
	}
	return nil
}
