package main

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

const (
	databaseName = "northwindrelay"
	username     = "postgres"
	password     = "SupplyChain!2026"
	defaultImage = "postgres:18-alpine"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	image := strings.TrimSpace(os.Getenv("POSTGRESQL_DEMO_IMAGE"))
	if image == "" {
		image = defaultImage
	}

	container, err := postgres.Run(ctx,
		image,
		postgres.WithDatabase(databaseName),
		postgres.WithUsername(username),
		postgres.WithPassword(password),
	)
	if err != nil {
		log.Printf("start PostgreSQL container: %v", err)
		return 1
	}
	defer func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			log.Printf("terminate PostgreSQL container: %v", err)
		}
	}()

	host, err := container.Host(ctx)
	if err != nil {
		log.Printf("get container host: %v", err)
		return 1
	}
	mappedPort, err := container.MappedPort(ctx, "5432/tcp")
	if err != nil {
		log.Printf("get container port: %v", err)
		return 1
	}
	portStr := mappedPort.Port()
	conn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		username, password, host, portStr, databaseName)

	db, err := sql.Open("pgx", conn)
	if err != nil {
		log.Printf("open PostgreSQL connection: %v", err)
		return 1
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("close PostgreSQL connection: %v", err)
		}
	}()

	if err := waitForDatabase(ctx, db); err != nil {
		log.Printf("wait for PostgreSQL readiness: %v", err)
		return 1
	}
	if err := execBatches(ctx, db, seedSQL); err != nil {
		log.Printf("seed database: %v", err)
		return 1
	}
	if err := verifySeeded(ctx, db); err != nil {
		log.Printf("verify seeded database: %v", err)
		return 1
	}

	if err := writeDemoEnv(host, portStr); err != nil {
		log.Printf("update .env with demo connection: %v", err)
		return 1
	}

	printInstructions(host, portStr)

	fmt.Println()
	fmt.Println("PostgreSQL is ready. Press Enter to stop the demo and remove the container.")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	return 0
}

func waitForDatabase(ctx context.Context, db *sql.DB) error {
	deadline := time.Now().Add(60 * time.Second)
	var lastErr error

	for {
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := db.PingContext(pingCtx)
		cancel()
		if err == nil {
			return nil
		}

		lastErr = err
		if time.Now().After(deadline) {
			return fmt.Errorf("database did not become ready within 60s: %w", lastErr)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("database readiness canceled: %w", ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func execBatches(ctx context.Context, db *sql.DB, script string) error {
	for batch := range strings.SplitSeq(script, "\n;\n") {
		batch = strings.TrimSpace(batch)
		if batch == "" {
			continue
		}
		if _, err := db.ExecContext(ctx, batch); err != nil {
			return fmt.Errorf("execute batch %q: %w", firstLine(batch), err)
		}
	}
	return nil
}

func verifySeeded(ctx context.Context, db *sql.DB) error {
	var tableCount int
	if err := db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM information_schema.tables
WHERE table_schema IN ('ops', 'finance')`).Scan(&tableCount); err != nil {
		return err
	}
	if tableCount == 0 {
		return fmt.Errorf("%s has zero user tables after seeding", databaseName)
	}
	fmt.Printf("Seeded %s with %d user tables.\n", databaseName, tableCount)
	return nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	if len(line) > 80 {
		return line[:80] + "..."
	}
	return line
}

func writeDemoEnv(host, port string) error {
	replacements := map[string]string{
		"POSTGRESQL_HOST":         host,
		"POSTGRESQL_PORT":         port,
		"POSTGRESQL_DATABASE":     databaseName,
		"POSTGRESQL_USER":         username,
		"POSTGRESQL_PASSWORD":     password,
		"POSTGRESQL_SSLMODE":      "disable",
		"POSTGRESQL_ACCESS_LEVEL": "READONLY",
	}
	defaults := map[string]string{
		"POSTGRESQL_MCP_SERVER_DIR": "../..",
	}
	order := []string{
		"POSTGRESQL_HOST",
		"POSTGRESQL_PORT",
		"POSTGRESQL_DATABASE",
		"POSTGRESQL_USER",
		"POSTGRESQL_PASSWORD",
		"POSTGRESQL_SSLMODE",
		"POSTGRESQL_ACCESS_LEVEL",
		"POSTGRESQL_MCP_SERVER_DIR",
	}

	data, err := os.ReadFile(".env")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	seen := make(map[string]bool)
	lines := make([]string, 0)
	if len(data) > 0 {
		for line := range strings.SplitSeq(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
			key := envLineKey(line)
			if value, ok := replacements[key]; ok {
				lines = append(lines, key+"="+value)
				seen[key] = true
				continue
			}
			if _, ok := defaults[key]; ok {
				seen[key] = true
			}
			lines = append(lines, line)
		}
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
	}

	missing := make([]string, 0)
	for _, key := range order {
		if seen[key] {
			continue
		}
		if value, ok := replacements[key]; ok {
			missing = append(missing, key+"="+value)
			continue
		}
		if value, ok := defaults[key]; ok {
			missing = append(missing, key+"="+value)
		}
	}
	if len(missing) > 0 {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, "# Updated by supply-chain demo starter.")
		lines = append(lines, missing...)
	}

	return os.WriteFile(".env", []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

func envLineKey(line string) string {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return ""
	}
	key, _, ok := strings.Cut(line, "=")
	if !ok {
		return ""
	}
	return strings.TrimSpace(key)
}

func printInstructions(host, port string) {
	fmt.Println("PostgreSQL demo database is running.")
	fmt.Println("Updated .env with the current container connection.")
	fmt.Println()
	fmt.Println("Environment for postgresql-mcp:")
	fmt.Printf("POSTGRESQL_HOST=%s\n", host)
	fmt.Printf("POSTGRESQL_PORT=%s\n", port)
	fmt.Printf("POSTGRESQL_DATABASE=%s\n", databaseName)
	fmt.Println("POSTGRESQL_USER=postgres")
	fmt.Printf("POSTGRESQL_PASSWORD=%s\n", password)
	fmt.Println("POSTGRESQL_SSLMODE=disable")
	fmt.Println("POSTGRESQL_ACCESS_LEVEL=READONLY")
	fmt.Println()
	fmt.Println("Example MCP server config:")
	fmt.Println(`{
  "mcpServers": {
    "postgresql-northwind-relay": {
      "command": "go",
      "args": ["run", "./cmd/postgresql-mcp"],
      "env": {
        "POSTGRESQL_HOST": "` + host + `",
        "POSTGRESQL_PORT": "` + port + `",
        "POSTGRESQL_DATABASE": "` + databaseName + `",
        "POSTGRESQL_USER": "postgres",
        "POSTGRESQL_PASSWORD": "` + password + `",
        "POSTGRESQL_SSLMODE": "disable",
        "POSTGRESQL_ACCESS_LEVEL": "READONLY"
      }
    }
  }
}`)
	fmt.Println()
	fmt.Println("Agent prompt:")
	fmt.Println(`You are an operations detective for Northwind Relay. Use the postgresql-northwind-relay MCP server to inspect the schema and data. Build a concise findings report that identifies the most important operational, financial, and data-quality risks. Include the SQL evidence behind each finding and recommend the next three actions.`)
}

const seedSQL = `
CREATE SCHEMA IF NOT EXISTS ops;
CREATE SCHEMA IF NOT EXISTS finance;

DROP TABLE IF EXISTS finance.Payments CASCADE;
DROP TABLE IF EXISTS ops.Shipments CASCADE;
DROP TABLE IF EXISTS ops.SalesOrderLines CASCADE;
DROP TABLE IF EXISTS ops.SalesOrders CASCADE;
DROP TABLE IF EXISTS ops.QualityIncidents CASCADE;
DROP TABLE IF EXISTS ops.PurchaseOrderLines CASCADE;
DROP TABLE IF EXISTS ops.PurchaseOrders CASCADE;
DROP TABLE IF EXISTS ops.InventorySnapshots CASCADE;
DROP TABLE IF EXISTS ops.Products CASCADE;
DROP TABLE IF EXISTS ops.Warehouses CASCADE;
DROP TABLE IF EXISTS ops.Customers CASCADE;
DROP TABLE IF EXISTS ops.Vendors CASCADE;

CREATE TABLE ops.Vendors (
    VendorID INT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    VendorName VARCHAR(120) NOT NULL,
    CountryCode CHAR(2) NOT NULL,
    RiskTier VARCHAR(20) NOT NULL,
    OnTimeSLA NUMERIC(5,2) NOT NULL,
    PaymentTermsDays INT NOT NULL
);

CREATE TABLE ops.Warehouses (
    WarehouseID INT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    WarehouseCode VARCHAR(12) NOT NULL UNIQUE,
    Region VARCHAR(40) NOT NULL,
    CapacityUnits INT NOT NULL
);

CREATE TABLE ops.Products (
    ProductID INT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    SKU VARCHAR(40) NOT NULL UNIQUE,
    ProductName VARCHAR(140) NOT NULL,
    Category VARCHAR(60) NOT NULL,
    StandardCost NUMERIC(12,2) NOT NULL,
    ListPrice NUMERIC(12,2) NOT NULL,
    ReorderPoint INT NOT NULL,
    PreferredVendorID INT NOT NULL,
    CONSTRAINT FK_Products_PreferredVendor FOREIGN KEY (PreferredVendorID) REFERENCES ops.Vendors(VendorID)
);

CREATE TABLE ops.InventorySnapshots (
    SnapshotID INT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    ProductID INT NOT NULL,
    WarehouseID INT NOT NULL,
    SnapshotDate DATE NOT NULL,
    OnHandUnits INT NOT NULL,
    ReservedUnits INT NOT NULL,
    DamagedUnits INT NOT NULL,
    CONSTRAINT FK_Inventory_Product FOREIGN KEY (ProductID) REFERENCES ops.Products(ProductID),
    CONSTRAINT FK_Inventory_Warehouse FOREIGN KEY (WarehouseID) REFERENCES ops.Warehouses(WarehouseID)
);

CREATE TABLE ops.PurchaseOrders (
    PurchaseOrderID INT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    VendorID INT NOT NULL,
    OrderDate DATE NOT NULL,
    ExpectedDate DATE NOT NULL,
    ActualReceiptDate DATE,
    Status VARCHAR(20) NOT NULL,
    CONSTRAINT FK_PurchaseOrders_Vendor FOREIGN KEY (VendorID) REFERENCES ops.Vendors(VendorID)
);

CREATE TABLE ops.PurchaseOrderLines (
    PurchaseOrderLineID INT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    PurchaseOrderID INT NOT NULL,
    ProductID INT NOT NULL,
    OrderedUnits INT NOT NULL,
    ReceivedUnits INT NOT NULL,
    UnitCost NUMERIC(12,2) NOT NULL,
    CONSTRAINT FK_POLines_PO FOREIGN KEY (PurchaseOrderID) REFERENCES ops.PurchaseOrders(PurchaseOrderID),
    CONSTRAINT FK_POLines_Product FOREIGN KEY (ProductID) REFERENCES ops.Products(ProductID)
);

CREATE TABLE ops.QualityIncidents (
    IncidentID INT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    ProductID INT NOT NULL,
    VendorID INT NOT NULL,
    WarehouseID INT NOT NULL,
    IncidentDate DATE NOT NULL,
    Severity VARCHAR(20) NOT NULL,
    DefectCode VARCHAR(40) NOT NULL,
    AffectedUnits INT NOT NULL,
    RootCause VARCHAR(200),
    CONSTRAINT FK_Quality_Product FOREIGN KEY (ProductID) REFERENCES ops.Products(ProductID),
    CONSTRAINT FK_Quality_Vendor FOREIGN KEY (VendorID) REFERENCES ops.Vendors(VendorID),
    CONSTRAINT FK_Quality_Warehouse FOREIGN KEY (WarehouseID) REFERENCES ops.Warehouses(WarehouseID)
);

CREATE TABLE ops.Customers (
    CustomerID INT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    CustomerName VARCHAR(140) NOT NULL,
    Segment VARCHAR(40) NOT NULL,
    Region VARCHAR(40) NOT NULL,
    CreditLimit NUMERIC(12,2) NOT NULL,
    RelatedVendorID INT,
    CONSTRAINT FK_Customers_RelatedVendor FOREIGN KEY (RelatedVendorID) REFERENCES ops.Vendors(VendorID)
);

CREATE TABLE ops.SalesOrders (
    SalesOrderID INT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    CustomerID INT NOT NULL,
    OrderDate DATE NOT NULL,
    RequestedShipDate DATE NOT NULL,
    ActualShipDate DATE,
    Status VARCHAR(20) NOT NULL,
    CONSTRAINT FK_SalesOrders_Customer FOREIGN KEY (CustomerID) REFERENCES ops.Customers(CustomerID)
);

CREATE TABLE ops.Shipments (
    ShipmentID INT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    SalesOrderID INT NOT NULL,
    WarehouseID INT NOT NULL,
    Carrier VARCHAR(80) NOT NULL,
    DepartedAt TIMESTAMP,
    DeliveredAt TIMESTAMP,
    TemperatureExcursionMinutes INT NOT NULL,
    FreightCost NUMERIC(12,2) NOT NULL,
    Status VARCHAR(20) NOT NULL,
    CONSTRAINT FK_Shipments_SalesOrder FOREIGN KEY (SalesOrderID) REFERENCES ops.SalesOrders(SalesOrderID),
    CONSTRAINT FK_Shipments_Warehouse FOREIGN KEY (WarehouseID) REFERENCES ops.Warehouses(WarehouseID)
);

CREATE TABLE ops.SalesOrderLines (
    SalesOrderLineID INT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    SalesOrderID INT NOT NULL,
    ProductID INT NOT NULL,
    OrderedUnits INT NOT NULL,
    UnitPrice NUMERIC(12,2) NOT NULL,
    DiscountPct NUMERIC(5,2) NOT NULL,
    CONSTRAINT FK_SOLines_SalesOrder FOREIGN KEY (SalesOrderID) REFERENCES ops.SalesOrders(SalesOrderID),
    CONSTRAINT FK_SOLines_Product FOREIGN KEY (ProductID) REFERENCES ops.Products(ProductID)
);

CREATE TABLE finance.Payments (
    PaymentID INT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    CustomerID INT NOT NULL,
    SalesOrderID INT NOT NULL,
    PaymentDate DATE NOT NULL,
    Amount NUMERIC(12,2) NOT NULL,
    Method VARCHAR(30) NOT NULL,
    Status VARCHAR(20) NOT NULL,
    CONSTRAINT FK_Payments_Customer FOREIGN KEY (CustomerID) REFERENCES ops.Customers(CustomerID),
    CONSTRAINT FK_Payments_SalesOrder FOREIGN KEY (SalesOrderID) REFERENCES ops.SalesOrders(SalesOrderID)
);

CREATE INDEX IX_Inventory_ProductWarehouseDate ON ops.InventorySnapshots(ProductID, WarehouseID, SnapshotDate);
CREATE INDEX IX_PurchaseOrders_VendorDates ON ops.PurchaseOrders(VendorID, ExpectedDate, ActualReceiptDate);
CREATE INDEX IX_Quality_VendorProductDate ON ops.QualityIncidents(VendorID, ProductID, IncidentDate);
CREATE INDEX IX_SalesOrders_CustomerDates ON ops.SalesOrders(CustomerID, RequestedShipDate, ActualShipDate);
CREATE INDEX IX_Shipments_OrderWarehouse ON ops.Shipments(SalesOrderID, WarehouseID, DeliveredAt);

INSERT INTO ops.Vendors (VendorName, CountryCode, RiskTier, OnTimeSLA, PaymentTermsDays) VALUES
('Aster Components', 'DE', 'Low', 96.50, 45),
('Blue Harbor Plastics', 'NL', 'Medium', 92.00, 30),
('Caldera MicroWorks', 'US', 'Low', 95.00, 30),
('Driftline Fabrication', 'CN', 'High', 88.00, 60),
('Echo Valley Packaging', 'PL', 'Low', 97.00, 21);

INSERT INTO ops.Warehouses (WarehouseCode, Region, CapacityUnits) VALUES
('AMS-1', 'Europe North', 24000),
('WAW-1', 'Europe East', 16000),
('RNO-1', 'North America West', 22000);

INSERT INTO ops.Products (SKU, ProductName, Category, StandardCost, ListPrice, ReorderPoint, PreferredVendorID) VALUES
('NR-SENSOR-9', 'Cold Chain Sensor v9', 'Electronics', 18.20, 39.00, 900, 3),
('NR-VALVE-A2', 'Adaptive Flow Valve A2', 'Industrial', 42.00, 87.00, 420, 4),
('NR-INSUL-4', 'Thermal Insulation Panel 4cm', 'Materials', 7.30, 16.00, 1200, 2),
('NR-BOX-SMART', 'Smart Returnable Crate', 'Packaging', 11.40, 25.00, 800, 5),
('NR-PUMP-MINI', 'Miniature Circulation Pump', 'Industrial', 31.50, 69.00, 350, 4),
('NR-GATEWAY-LTE', 'LTE Telemetry Gateway', 'Electronics', 57.00, 129.00, 260, 1);

INSERT INTO ops.InventorySnapshots (ProductID, WarehouseID, SnapshotDate, OnHandUnits, ReservedUnits, DamagedUnits) VALUES
(1, 1, '2026-05-31', 1320, 610, 6),
(1, 3, '2026-05-31', 780, 700, 4),
(2, 1, '2026-05-31', 510, 420, 37),
(2, 2, '2026-05-31', 260, 235, 31),
(3, 1, '2026-05-31', 1900, 620, 18),
(3, 2, '2026-05-31', 760, 540, 14),
(4, 1, '2026-05-31', 1100, 460, 2),
(4, 3, '2026-05-31', 940, 410, 1),
(5, 1, '2026-05-31', 390, 370, 22),
(5, 3, '2026-05-31', 210, 205, 17),
(6, 1, '2026-05-31', 320, 180, 1),
(6, 3, '2026-05-31', 260, 240, 0);

INSERT INTO ops.PurchaseOrders (VendorID, OrderDate, ExpectedDate, ActualReceiptDate, Status) VALUES
(4, '2026-04-02', '2026-04-22', '2026-05-03', 'Received'),
(4, '2026-04-18', '2026-05-08', '2026-05-27', 'Received'),
(4, '2026-05-12', '2026-06-01', NULL, 'Late'),
(3, '2026-05-01', '2026-05-17', '2026-05-16', 'Received'),
(1, '2026-05-03', '2026-05-21', '2026-05-22', 'Received'),
(2, '2026-05-05', '2026-05-24', '2026-05-24', 'Received'),
(5, '2026-05-06', '2026-05-19', '2026-05-18', 'Received');

INSERT INTO ops.PurchaseOrderLines (PurchaseOrderID, ProductID, OrderedUnits, ReceivedUnits, UnitCost) VALUES
(1, 2, 700, 640, 45.50),
(1, 5, 480, 455, 33.20),
(2, 2, 620, 590, 47.10),
(2, 5, 400, 375, 34.40),
(3, 2, 800, 0, 48.00),
(3, 5, 520, 0, 35.10),
(4, 1, 1000, 1000, 18.60),
(5, 6, 360, 360, 58.00),
(6, 3, 1600, 1600, 7.80),
(7, 4, 1400, 1400, 11.60);

INSERT INTO ops.QualityIncidents (ProductID, VendorID, WarehouseID, IncidentDate, Severity, DefectCode, AffectedUnits, RootCause) VALUES
(2, 4, 1, '2026-05-05', 'High', 'PRESSURE_LEAK', 36, 'Valve seal drift after transit heat exposure'),
(5, 4, 3, '2026-05-09', 'Medium', 'NOISY_BEARING', 18, 'Bearing tolerance outside purchase specification'),
(2, 4, 2, '2026-05-29', 'High', 'PRESSURE_LEAK', 29, 'Repeated lot failure, same tooling line'),
(5, 4, 1, '2026-05-30', 'High', 'POWER_SPIKE', 21, 'Controller board substitution by vendor'),
(3, 2, 1, '2026-05-26', 'Low', 'EDGE_CRACK', 12, 'Forklift damage during receiving');

INSERT INTO ops.Customers (CustomerName, Segment, Region, CreditLimit, RelatedVendorID) VALUES
('Alpine Grocery Group', 'Enterprise', 'Europe North', 180000, NULL),
('Boreal Pharma Logistics', 'Enterprise', 'Europe North', 250000, NULL),
('Cirrus Field Labs', 'Midmarket', 'North America West', 90000, NULL),
('Driftline Trading HK', 'Distributor', 'Asia Pacific', 75000, 4),
('Evergreen Meal Kits', 'Midmarket', 'Europe East', 65000, NULL);

INSERT INTO ops.SalesOrders (CustomerID, OrderDate, RequestedShipDate, ActualShipDate, Status) VALUES
(1, '2026-05-10', '2026-05-18', '2026-05-18', 'Shipped'),
(2, '2026-05-13', '2026-05-23', '2026-05-27', 'Shipped'),
(2, '2026-05-24', '2026-06-03', NULL, 'Blocked'),
(3, '2026-05-15', '2026-05-25', '2026-05-26', 'Shipped'),
(4, '2026-05-16', '2026-05-24', '2026-05-24', 'Shipped'),
(5, '2026-05-21', '2026-05-31', NULL, 'Picking');

INSERT INTO ops.Shipments (SalesOrderID, WarehouseID, Carrier, DepartedAt, DeliveredAt, TemperatureExcursionMinutes, FreightCost, Status) VALUES
(1, 1, 'PolarLine Freight', '2026-05-17 07:15:00', '2026-05-18 10:40:00', 0, 920.00, 'Delivered'),
(2, 1, 'PolarLine Freight', '2026-05-23 16:20:00', '2026-05-27 12:05:00', 155, 1280.00, 'Delivered'),
(3, 1, 'PolarLine Freight', NULL, NULL, 0, 0.00, 'Blocked'),
(4, 3, 'Western Rail Express', '2026-05-24 06:00:00', '2026-05-26 09:30:00', 0, 830.00, 'Delivered'),
(5, 2, 'Azure Sea Forwarding', '2026-05-23 23:10:00', '2026-05-24 22:15:00', 0, 1110.00, 'Delivered'),
(6, 2, 'Vistula Road Link', NULL, NULL, 0, 0.00, 'Picking');

INSERT INTO ops.SalesOrderLines (SalesOrderID, ProductID, OrderedUnits, UnitPrice, DiscountPct) VALUES
(1, 1, 300, 39.00, 2.00),
(1, 3, 500, 16.00, 0.00),
(2, 2, 280, 87.00, 4.00),
(2, 5, 190, 69.00, 4.00),
(3, 2, 420, 87.00, 6.00),
(3, 5, 260, 69.00, 6.00),
(4, 6, 210, 129.00, 3.00),
(5, 2, 120, 87.00, 18.00),
(5, 5, 90, 69.00, 18.00),
(6, 3, 460, 16.00, 2.00),
(6, 4, 320, 25.00, 1.00);

INSERT INTO finance.Payments (CustomerID, SalesOrderID, PaymentDate, Amount, Method, Status) VALUES
(1, 1, '2026-05-20', 19220.00, 'Wire', 'Cleared'),
(2, 2, '2026-05-30', 35933.40, 'Wire', 'Cleared'),
(3, 4, '2026-05-28', 26277.30, 'Card', 'Cleared'),
(4, 5, '2026-05-17', 13615.20, 'Wire', 'Reversed'),
(4, 5, '2026-05-25', 13615.20, 'Wire', 'Cleared');
`
