//! Go's `json.Marshal`, which the shelf's stored JSON (a tick's `leaves`, a
//! receipt, a book checkpoint) is written in: members in the order given,
//! `&`, `<`, `>`, U+2028 and U+2029 escaped, and every control character but
//! `\n`, `\r` and `\t` as `\u00XX`.

use serde_json::Value;

/// Go's `json.Marshal`.
pub(crate) fn marshal(value: &Value) -> String {
    let mut out = String::new();
    write_value(value, &mut out);
    out
}

fn write_value(value: &Value, out: &mut String) {
    match value {
        Value::Array(items) => {
            out.push('[');
            for (i, item) in items.iter().enumerate() {
                if i > 0 {
                    out.push(',');
                }
                write_value(item, out);
            }
            out.push(']');
        }
        Value::Object(members) => {
            out.push('{');
            for (i, (k, v)) in members.iter().enumerate() {
                if i > 0 {
                    out.push(',');
                }
                write_string(k, out);
                out.push(':');
                write_value(v, out);
            }
            out.push('}');
        }
        Value::String(s) => write_string(s, out),
        other => out.push_str(&other.to_string()),
    }
}

fn write_string(s: &str, out: &mut String) {
    out.push('"');
    for c in s.chars() {
        match c {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\t' => out.push_str("\\t"),
            '&' | '<' | '>' | '\u{2028}' | '\u{2029}' => {
                out.push_str(&format!("\\u{:04x}", c as u32))
            }
            c if (c as u32) < 0x20 => out.push_str(&format!("\\u{:04x}", c as u32)),
            c => out.push(c),
        }
    }
    out.push('"');
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn writes_as_go_does() {
        let v = json!({"b": [1, "x<&>\u{1}"], "a": {}, "c": []});
        assert_eq!(
            marshal(&v),
            "{\"b\":[1,\"x\\u003c\\u0026\\u003e\\u0001\"],\"a\":{},\"c\":[]}"
        );
    }
}
