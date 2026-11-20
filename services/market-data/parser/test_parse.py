import base64
import io
import unittest
from parse import parse

CSV = b"date,o,h,l,c,v\n2024-01-01T00:00:00Z,10,12,9,11,5\n2024-01-01T00:01:00Z,11,13,10,12,6\n"
MAPPING = {"timestamp_column":"date", "timezone":"UTC", "timestamp_unit":"iso8601",
           "columns":{"open":"o","high":"h","low":"l","close":"c","volume":"v"}}

class TestParser(unittest.TestCase):
    def payload(self, data=CSV, mapping=None, format="csv"):
        return {"format":format,"data":base64.b64encode(data).decode(),"mapping":mapping,
                "max_upload_bytes":100000,"max_rows":100}

    def test_inspection_and_exact_decimal_normalization(self):
        p = parse(self.payload())
        self.assertEqual(p["row_count"], 2)
        out = parse(self.payload(CSV.replace(b",10,12,9,11,", b",10.000000000000000001,12,9,11,"), MAPPING))
        self.assertEqual(out["rows"][0][1], "10.000000000000000001")
        self.assertEqual(out["rows"][1][0], "2024-01-01T00:01:00Z")

    def test_invalid_schema_prices_order_and_limits(self):
        for raw in [CSV.replace(b"date,o", b"o,o"), CSV.replace(b",10,12,9,11,", b",NaN,12,9,11,"),
                    CSV.replace(b",10,12,9,11,", b",10,2,9,11,"), CSV.replace(b"00:01:00", b"00:00:00"),
                    CSV.replace(b"00:01:00", b"00:01:00.1234567"), CSV+b"bad,row\n"]:
            with self.subTest(raw=raw):
                with self.assertRaises(Exception):
                    parse(self.payload(raw, MAPPING))
        p = self.payload()
        p["max_rows"] = 1
        with self.assertRaises(ValueError):
            parse(p)

    def test_parquet_is_actually_decoded_and_nested_values_rejected(self):
        import pyarrow as pa
        import pyarrow.parquet as pq
        sink = pa.BufferOutputStream()
        table = pa.table({"date":["2024-01-01T00:00:00Z"],"o":[10],"h":[12],"l":[9],"c":[11],"v":[5]})
        pq.write_table(table, sink, compression="snappy")
        out = parse(self.payload(sink.getvalue().to_pybytes(), MAPPING, "parquet"))
        self.assertEqual(out["rows"][0][1:], ["10","12","9","11","5"])
        sink = pa.BufferOutputStream()
        pq.write_table(pa.table({"nested":[[1,2,3]]}),sink)
        with self.assertRaises(ValueError):
            parse(self.payload(sink.getvalue().to_pybytes(), None, "parquet"))

    def test_local_ambiguous_time_rejected(self):
        m = {**MAPPING, "timezone":"Europe/Paris"}
        raw = CSV.replace(b"2024-01-01T00:00:00Z", b"2024-10-27T02:30:00")
        with self.assertRaises(ValueError):
            parse(self.payload(raw,m))

if __name__ == "__main__":
    unittest.main()
