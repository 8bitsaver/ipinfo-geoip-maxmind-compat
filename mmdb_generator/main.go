package main

import (
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/netip"
	"os"
	// "strings"

	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"
)

var (
	csvFile          string
	dbOutputDir      string
)

func init() {
	flag.StringVar(&csvFile, "csv-in", "country.csv", "Path to the country.csv")
	flag.StringVar(&dbOutputDir, "mmdb-out", "country.mmdb", "Output path to the country.mmdb")
	flag.Parse()
}


// IsStringSlicesEqual tells whether a and b contain the same elements.
// A nil argument is equivalent to an empty slice.
func isStringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if v != b[i] {
			return false
		}
	}
	return true
}

func indexOf(word string, data []string) int {
	for k, v := range data {
		if word == v {
			return k
		}
	}
	return -1
}

// IpRangeToCIDR refer from https://gist.github.com/P-A-R-U-S/a090dd90c5104ce85a29c32669dac107?permalink_comment_id=4362208#gistcomment-4362208
func IpRangeToCIDR(start, end string) ([]string, error) {
	ips, err := netip.ParseAddr(start)
	if err != nil {
		return nil, err
	}
	ipe, err := netip.ParseAddr(end)
	if err != nil {
		return nil, err
	}

	isV4 := ips.Is4()
	if isV4 != ipe.Is4() {
		return nil, errors.New("start and end types are different")
	}
	if ips.Compare(ipe) > 0 {
		return nil, errors.New("start > end")
	}

	var (
		ipsInt = new(big.Int).SetBytes(ips.AsSlice())
		ipeInt = new(big.Int).SetBytes(ipe.AsSlice())
		nextIp = new(big.Int)
		maxBit = new(big.Int)
		cmpSh  = new(big.Int)
		bits   = new(big.Int)
		mask   = new(big.Int)
		one    = big.NewInt(1)
		buf    []byte
		cidr   []string
		bitSh  uint
	)
	if isV4 {
		maxBit.SetUint64(32)
		buf = make([]byte, 4)
	} else if ips.Is6() {
		maxBit.SetUint64(128)
		buf = make([]byte, 16)
	} else {
		return nil, errors.New("start is neither IPv4 nor IPv6")
	}

	for {
		bits.SetUint64(1)
		mask.SetUint64(1)
		for bits.Cmp(maxBit) < 0 {
			nextIp.Or(ipsInt, mask)

			bitSh = uint(bits.Uint64())
			cmpSh.Lsh(cmpSh.Rsh(ipsInt, bitSh), bitSh)
			if (nextIp.Cmp(ipeInt) > 0) || (cmpSh.Cmp(ipsInt) != 0) {
				bits.Sub(bits, one)
				mask.Rsh(mask, 1)
				break
			}
			bits.Add(bits, one)
			mask.Add(mask.Lsh(mask, 1), one)
		}

		addr, _ := netip.AddrFromSlice(ipsInt.FillBytes(buf))
		cidr = append(cidr, addr.String()+"/"+bits.Sub(maxBit, bits).String())

		if nextIp.Or(ipsInt, mask); nextIp.Cmp(ipeInt) >= 0 {
			break
		}
		ipsInt.Add(nextIp, one)
	}
	return cidr, nil
}

// GenIPtoCountry refer from https://github.com/maxmind/mmdbwriter/blob/main/examples/asn-writer/main.go
// expected csv header is {"start_ip", "end_ip", "country", "country_name", "continent", "continent_name"}
func GenIPtoCountry(csvFile, dbFile string) {
	writer, err := mmdbwriter.New(
		// https://pkg.go.dev/github.com/maxmind/mmdbwriter
		mmdbwriter.Options{
			IPVersion:    6,
			DatabaseType: "ipinfo country.mmdb",
			RecordSize:   24,
			Languages:    []string{"en"},
			Description:  map[string]string{"en": "ipinfo country.mmdb"},
		},
	)
	if err != nil {
		log.Fatal(err)
	}

	for _, file := range []string{csvFile} {
		fh, err := os.Open(file)
		if err != nil {
			log.Fatal(err)
		}

		r := csv.NewReader(fh)

		// first line: header
		header, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatal(err)
		}
		startIPIndex := indexOf("start_ip", header)
		endIPIndex := indexOf("end_ip", header)
		countryIndex := indexOf("country", header)
		countryNameIndex := indexOf("country_name", header)
		continentIndex := indexOf("continent", header)
		continentNameIndex := indexOf("continent_name", header)
		if startIPIndex == -1 || endIPIndex == -1 ||
			countryIndex == -1 || countryNameIndex == -1 ||
			continentIndex == -1 || continentNameIndex == -1 {
			log.Fatalf("unsupported CSV content")
		} else {
			fmt.Printf("CSV header: %s\n", header)
		}

		for {
			row, err := r.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				log.Fatal(err)
			}

			if len(row) != 6 {
				log.Fatalf("unexpected CSV rows: %v", row)
			}

			cidrs, err := IpRangeToCIDR(row[startIPIndex], row[endIPIndex])
			if err != nil {
				log.Fatal(err)
			}
			// fmt.Println(strings.Join(cidrs, "\n"))

			for _, value := range cidrs {
				_, network, err := net.ParseCIDR(value)
				if err != nil {
					log.Fatal(err)
				}

				record := mmdbtype.Map{
					"continent": mmdbtype.Map{
						"code": mmdbtype.String(row[continentIndex]),
						"names": mmdbtype.Map{
							"en": mmdbtype.String(row[continentNameIndex]),
						},
					},
					"country": mmdbtype.Map{
						"iso_code": mmdbtype.String(row[countryIndex]),
						"names": mmdbtype.Map{
							"en": mmdbtype.String(row[countryNameIndex]),
						},
					},
				}

				err = writer.Insert(network, record)
				if err != nil {
					log.Fatal(err)
				}
			}
		}
	}

	fh, err := os.Create(dbFile)
	if err != nil {
		log.Fatal(err)
	}

	_, err = writer.WriteTo(fh)
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	// sample https://github.com/ipinfo/sample-database/blob/main/IP%20to%20Country
	// Spec and test data https://github.com/maxmind/MaxMind-DB
	// default, GenIPtoCountry("country.csv", "country.mmdb")
	GenIPtoCountry(csvFile, dbOutputDir)
}
