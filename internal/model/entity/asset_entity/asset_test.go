package asset_entity

import (
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
)

func TestDatabaseDriverDefaultPort(t *testing.T) {
	convey.Convey("DatabaseDriver.DefaultPort", t, func() {
		convey.So(DriverMySQL.DefaultPort(), convey.ShouldEqual, 3306)
		convey.So(DriverPostgreSQL.DefaultPort(), convey.ShouldEqual, 5432)
		convey.So(DriverMSSQL.DefaultPort(), convey.ShouldEqual, 1433)
		convey.So(DriverSQLite.DefaultPort(), convey.ShouldEqual, 0)
	})
}

func TestAsset_Validate(t *testing.T) {
	convey.Convey("资产校验", t, func() {
		convey.Convey("名称为空时应返回错误", func() {
			a := &Asset{Type: AssetTypeSSH}
			err := a.Validate()
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "名称")
		})

		convey.Convey("类型为空时应返回错误", func() {
			a := &Asset{Name: "test"}
			err := a.Validate()
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "类型")
		})

		convey.Convey("扩展资产类型校验应通过", func() {
			a := &Asset{Name: "test", Type: "oss"}
			err := a.Validate()
			assert.NoError(t, err)
		})

		convey.Convey("SSH类型缺少Config应返回错误", func() {
			a := &Asset{Name: "test", Type: AssetTypeSSH}
			err := a.Validate()
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "SSH")
		})

		convey.Convey("SSH类型配置完整时校验通过", func() {
			a := &Asset{Name: "test", Type: AssetTypeSSH}
			err := a.SetSSHConfig(&SSHConfig{
				Host:     "192.168.1.1",
				Port:     22,
				Username: "root",
				AuthType: AuthTypePassword,
			})
			assert.NoError(t, err)
			err = a.Validate()
			assert.NoError(t, err)
		})

		convey.Convey("SSH配置缺少host应返回错误", func() {
			a := &Asset{Name: "test", Type: AssetTypeSSH}
			_ = a.SetSSHConfig(&SSHConfig{
				Port:     22,
				Username: "root",
				AuthType: AuthTypePassword,
			})
			err := a.Validate()
			assert.Error(t, err)
		})
	})
}

func TestAsset_SSHConfig(t *testing.T) {
	convey.Convey("SSH配置序列化与反序列化", t, func() {
		convey.Convey("SetSSHConfig后GetSSHConfig应返回相同内容", func() {
			a := &Asset{Name: "test", Type: AssetTypeSSH}
			cfg := &SSHConfig{
				Host:     "10.0.0.1",
				Port:     2222,
				Username: "admin",
				AuthType: AuthTypeKey,
			}
			err := a.SetSSHConfig(cfg)
			assert.NoError(t, err)

			got, err := a.GetSSHConfig()
			assert.NoError(t, err)
			assert.Equal(t, cfg.Host, got.Host)
			assert.Equal(t, cfg.Port, got.Port)
			assert.Equal(t, cfg.Username, got.Username)
			assert.Equal(t, cfg.AuthType, got.AuthType)
		})

		convey.Convey("非SSH类型调用GetSSHConfig应返回错误", func() {
			a := &Asset{Name: "test", Type: "db"}
			_, err := a.GetSSHConfig()
			assert.Error(t, err)
		})

		convey.Convey("Config为空时GetSSHConfig应返回错误", func() {
			a := &Asset{Name: "test", Type: AssetTypeSSH}
			_, err := a.GetSSHConfig()
			assert.Error(t, err)
		})
	})
}

func TestAsset_IsSSH(t *testing.T) {
	convey.Convey("IsSSH判断", t, func() {
		convey.Convey("SSH类型返回true", func() {
			a := &Asset{Type: AssetTypeSSH}
			assert.True(t, a.IsSSH())
		})
		convey.Convey("其他类型返回false", func() {
			a := &Asset{Type: "db"}
			assert.False(t, a.IsSSH())
		})
	})
}

func TestAsset_CanConnect(t *testing.T) {
	convey.Convey("CanConnect判断", t, func() {
		convey.Convey("活跃的SSH资产且配置完整可连接", func() {
			a := &Asset{Name: "test", Type: AssetTypeSSH, Status: StatusActive}
			_ = a.SetSSHConfig(&SSHConfig{
				Host: "10.0.0.1", Port: 22, Username: "root", AuthType: AuthTypePassword,
			})
			assert.True(t, a.CanConnect())
		})

		convey.Convey("非活跃资产不可连接", func() {
			a := &Asset{Name: "test", Type: AssetTypeSSH, Status: StatusDeleted}
			_ = a.SetSSHConfig(&SSHConfig{
				Host: "10.0.0.1", Port: 22, Username: "root", AuthType: AuthTypePassword,
			})
			assert.False(t, a.CanConnect())
		})

		convey.Convey("非SSH类型不可连接", func() {
			a := &Asset{Name: "test", Type: "db", Status: StatusActive}
			assert.False(t, a.CanConnect())
		})
	})
}

func TestAsset_SerialConfig(t *testing.T) {
	convey.Convey("串口配置序列化与反序列化", t, func() {
		a := &Asset{Name: "serial", Type: AssetTypeSerial}
		cfg := &SerialConfig{
			PortPath:    "COM3",
			BaudRate:    115200,
			DataBits:    8,
			StopBits:    "1",
			Parity:      "none",
			FlowControl: "hardware",
		}

		err := a.SetSerialConfig(cfg)
		assert.NoError(t, err)

		got, err := a.GetSerialConfig()
		assert.NoError(t, err)
		assert.Equal(t, cfg.PortPath, got.PortPath)
		assert.Equal(t, cfg.BaudRate, got.BaudRate)
		assert.Equal(t, cfg.DataBits, got.DataBits)
		assert.Equal(t, cfg.StopBits, got.StopBits)
		assert.Equal(t, cfg.Parity, got.Parity)
		assert.Equal(t, cfg.FlowControl, got.FlowControl)
		assert.True(t, a.IsSerial())
	})
}

func TestValidateSerial(t *testing.T) {
	convey.Convey("串口资产校验", t, func() {
		newAsset := func(mutate func(*SerialConfig)) *Asset {
			cfg := &SerialConfig{
				PortPath:    "COM3",
				BaudRate:    115200,
				DataBits:    8,
				StopBits:    "1",
				Parity:      "none",
				FlowControl: "none",
			}
			if mutate != nil {
				mutate(cfg)
			}
			a := &Asset{Name: "serial", Type: AssetTypeSerial}
			assert.NoError(t, a.SetSerialConfig(cfg))
			return a
		}

		convey.Convey("配置完整时校验通过", func() {
			assert.NoError(t, newAsset(nil).Validate())
		})

		convey.Convey("端口路径为空时返回错误", func() {
			assert.Error(t, newAsset(func(cfg *SerialConfig) { cfg.PortPath = "" }).Validate())
		})

		convey.Convey("波特率无效时返回错误", func() {
			assert.Error(t, newAsset(func(cfg *SerialConfig) { cfg.BaudRate = 0 }).Validate())
		})

		convey.Convey("数据位无效时返回错误", func() {
			assert.Error(t, newAsset(func(cfg *SerialConfig) { cfg.DataBits = 9 }).Validate())
		})

		convey.Convey("停止位无效时返回错误", func() {
			assert.Error(t, newAsset(func(cfg *SerialConfig) { cfg.StopBits = "3" }).Validate())
		})

		convey.Convey("校验位无效时返回错误", func() {
			assert.Error(t, newAsset(func(cfg *SerialConfig) { cfg.Parity = "invalid" }).Validate())
		})

		convey.Convey("流控模式无效时返回错误", func() {
			assert.Error(t, newAsset(func(cfg *SerialConfig) { cfg.FlowControl = "software" }).Validate())
		})
	})
}

func TestAsset_CanConnectSerial(t *testing.T) {
	convey.Convey("串口资产连接能力判断", t, func() {
		convey.Convey("活跃且端口路径存在时可连接", func() {
			a := &Asset{Name: "serial", Type: AssetTypeSerial, Status: StatusActive}
			assert.NoError(t, a.SetSerialConfig(&SerialConfig{PortPath: "COM3", BaudRate: 115200, DataBits: 8, StopBits: "1", Parity: "none"}))
			assert.True(t, a.CanConnect())
		})

		convey.Convey("端口路径为空时不可连接", func() {
			a := &Asset{Name: "serial", Type: AssetTypeSerial, Status: StatusActive}
			assert.NoError(t, a.SetSerialConfig(&SerialConfig{PortPath: "", BaudRate: 115200, DataBits: 8, StopBits: "1", Parity: "none"}))
			assert.False(t, a.CanConnect())
		})

		convey.Convey("非活跃串口资产不可连接", func() {
			a := &Asset{Name: "serial", Type: AssetTypeSerial, Status: StatusDeleted}
			assert.NoError(t, a.SetSerialConfig(&SerialConfig{PortPath: "COM3", BaudRate: 115200, DataBits: 8, StopBits: "1", Parity: "none"}))
			assert.False(t, a.CanConnect())
		})
	})
}

func TestAsset_SSHAddress(t *testing.T) {
	convey.Convey("SSHAddress格式", t, func() {
		a := &Asset{Name: "test", Type: AssetTypeSSH}
		_ = a.SetSSHConfig(&SSHConfig{Host: "10.0.0.1", Port: 2222, Username: "root", AuthType: AuthTypePassword})

		addr, err := a.SSHAddress()
		assert.NoError(t, err)
		assert.Equal(t, "10.0.0.1:2222", addr)
	})
}

func TestRedisConfig(t *testing.T) {
	convey.Convey("Redis配置序列化与反序列化", t, func() {
		a := &Asset{Name: "cache", Type: AssetTypeRedis}
		cfg := &RedisConfig{
			Host:                  "redis.example.com",
			Port:                  6380,
			Username:              "default",
			Database:              2,
			TLS:                   true,
			TLSInsecure:           true,
			TLSServerName:         "redis.internal",
			TLSCAFile:             "/etc/redis/ca.pem",
			TLSCertFile:           "/etc/redis/client.pem",
			TLSKeyFile:            "/etc/redis/client-key.pem",
			CommandTimeoutSeconds: 7,
			ScanPageSize:          500,
			KeySeparator:          "/",
		}

		err := a.SetRedisConfig(cfg)
		assert.NoError(t, err)

		got, err := a.GetRedisConfig()
		assert.NoError(t, err)
		assert.Equal(t, cfg.Host, got.Host)
		assert.Equal(t, cfg.Port, got.Port)
		assert.Equal(t, cfg.Username, got.Username)
		assert.Equal(t, cfg.Database, got.Database)
		assert.Equal(t, cfg.TLS, got.TLS)
		assert.Equal(t, cfg.TLSInsecure, got.TLSInsecure)
		assert.Equal(t, cfg.TLSServerName, got.TLSServerName)
		assert.Equal(t, cfg.TLSCAFile, got.TLSCAFile)
		assert.Equal(t, cfg.TLSCertFile, got.TLSCertFile)
		assert.Equal(t, cfg.TLSKeyFile, got.TLSKeyFile)
		assert.Equal(t, cfg.CommandTimeoutSeconds, got.CommandTimeoutSeconds)
		assert.Equal(t, cfg.ScanPageSize, got.ScanPageSize)
		assert.Equal(t, cfg.KeySeparator, got.KeySeparator)
	})
}

func TestMongoDBConfig(t *testing.T) {
	convey.Convey("MongoDB配置序列化与反序列化", t, func() {
		convey.Convey("IsMongoDB类型判断", func() {
			a := &Asset{Type: AssetTypeMongoDB}
			assert.True(t, a.IsMongoDB())
			b := &Asset{Type: AssetTypeSSH}
			assert.False(t, b.IsMongoDB())
		})

		convey.Convey("SetMongoDBConfig后GetMongoDBConfig应返回相同内容", func() {
			a := &Asset{Name: "test", Type: AssetTypeMongoDB}
			cfg := &MongoDBConfig{
				Host:     "mongo.example.com",
				Port:     27017,
				Username: "admin",
				Password: "secret",
				Database: "mydb",
			}
			err := a.SetMongoDBConfig(cfg)
			assert.NoError(t, err)

			got, err := a.GetMongoDBConfig()
			assert.NoError(t, err)
			assert.Equal(t, cfg.Host, got.Host)
			assert.Equal(t, cfg.Port, got.Port)
			assert.Equal(t, cfg.Username, got.Username)
			assert.Equal(t, cfg.Database, got.Database)
		})

		convey.Convey("非MongoDB类型调用GetMongoDBConfig应返回错误", func() {
			a := &Asset{Name: "test", Type: AssetTypeSSH}
			_, err := a.GetMongoDBConfig()
			assert.Error(t, err)
		})

		convey.Convey("ConnectionURI模式配置序列化", func() {
			a := &Asset{Name: "test", Type: AssetTypeMongoDB}
			cfg := &MongoDBConfig{ //nolint:gosec // test data
				ConnectionURI: "mongodb://user:pass@host:27017/db",
			}
			err := a.SetMongoDBConfig(cfg)
			assert.NoError(t, err)

			got, err := a.GetMongoDBConfig()
			assert.NoError(t, err)
			assert.Equal(t, cfg.ConnectionURI, got.ConnectionURI)
		})
	})
}

func TestValidateMongoDB(t *testing.T) {
	convey.Convey("MongoDB资产校验", t, func() {
		convey.Convey("ConnectionURI模式校验通过", func() {
			a := &Asset{Name: "test", Type: AssetTypeMongoDB}
			_ = a.SetMongoDBConfig(&MongoDBConfig{
				ConnectionURI: "mongodb://localhost:27017",
			})
			err := a.Validate()
			assert.NoError(t, err)
		})

		convey.Convey("手动模式配置完整时校验通过", func() {
			a := &Asset{Name: "test", Type: AssetTypeMongoDB}
			_ = a.SetMongoDBConfig(&MongoDBConfig{
				Host: "localhost",
				Port: 27017,
			})
			err := a.Validate()
			assert.NoError(t, err)
		})

		convey.Convey("手动模式缺少Host应返回错误", func() {
			a := &Asset{Name: "test", Type: AssetTypeMongoDB}
			_ = a.SetMongoDBConfig(&MongoDBConfig{
				Port: 27017,
			})
			err := a.Validate()
			assert.Error(t, err)
		})

		convey.Convey("空URI且无Host应返回错误", func() {
			a := &Asset{Name: "test", Type: AssetTypeMongoDB}
			_ = a.SetMongoDBConfig(&MongoDBConfig{
				ConnectionURI: "",
				Port:          27017,
			})
			err := a.Validate()
			assert.Error(t, err)
		})
	})
}

func TestKafkaConfig(t *testing.T) {
	convey.Convey("Kafka配置序列化与反序列化", t, func() {
		a := &Asset{Name: "kafka", Type: AssetTypeKafka, Status: StatusActive}
		cfg := &KafkaConfig{
			Brokers:               []string{"broker-1:9092", "broker-2:9092"},
			ClientID:              "opskat-test",
			SASLMechanism:         KafkaSASLSCRAMSHA256,
			Username:              "alice",
			Password:              "encrypted",
			TLS:                   true,
			TLSInsecure:           true,
			RequestTimeoutSeconds: 10,
			MessagePreviewBytes:   2048,
			MessageFetchLimit:     50,
		}
		err := a.SetKafkaConfig(cfg)
		assert.NoError(t, err)

		got, err := a.GetKafkaConfig()
		assert.NoError(t, err)
		assert.Equal(t, cfg.Brokers, got.Brokers)
		assert.Equal(t, cfg.ClientID, got.ClientID)
		assert.Equal(t, cfg.SASLMechanism, got.SASLMechanism)
		assert.Equal(t, cfg.Username, got.Username)
		assert.Equal(t, cfg.TLS, got.TLS)
		assert.True(t, a.IsKafka())
		assert.True(t, a.CanConnect())
	})
}

func TestValidateKafka(t *testing.T) {
	convey.Convey("Kafka资产校验", t, func() {
		convey.Convey("明文连接配置完整时校验通过", func() {
			a := &Asset{Name: "kafka", Type: AssetTypeKafka}
			_ = a.SetKafkaConfig(&KafkaConfig{Brokers: []string{"localhost:9092"}})
			assert.NoError(t, a.Validate())
		})

		convey.Convey("broker缺少端口应返回错误", func() {
			a := &Asset{Name: "kafka", Type: AssetTypeKafka}
			_ = a.SetKafkaConfig(&KafkaConfig{Brokers: []string{"localhost"}})
			assert.Error(t, a.Validate())
		})

		convey.Convey("SASL启用时需要用户名和密码来源", func() {
			a := &Asset{Name: "kafka", Type: AssetTypeKafka}
			_ = a.SetKafkaConfig(&KafkaConfig{
				Brokers:       []string{"localhost:9092"},
				SASLMechanism: KafkaSASLPlain,
			})
			assert.Error(t, a.Validate())
		})

		convey.Convey("TLS证书和私钥必须成对配置", func() {
			a := &Asset{Name: "kafka", Type: AssetTypeKafka}
			_ = a.SetKafkaConfig(&KafkaConfig{
				Brokers:     []string{"localhost:9092"},
				TLSCertFile: "/tmp/client.pem",
			})
			assert.Error(t, a.Validate())
		})
	})
}

func TestValidateDatabaseMSSQL(t *testing.T) {
	convey.Convey("MSSQL driver validation", t, func() {
		convey.Convey("缺 host 应报错", func() {
			a := &Asset{Type: AssetTypeDatabase, Name: "x", GroupID: 1}
			cfg := &DatabaseConfig{Driver: DriverMSSQL, Port: 1433, Username: "sa"}
			convey.So(a.SetDatabaseConfig(cfg), convey.ShouldBeNil)
			convey.So(a.Validate().Error(), convey.ShouldContainSubstring, "主机")
		})
		convey.Convey("port=0 应报错", func() {
			a := &Asset{Type: AssetTypeDatabase, Name: "x", GroupID: 1}
			cfg := &DatabaseConfig{
				Driver: DriverMSSQL, Host: "localhost",
				Port: 0, Username: "sa",
			}
			convey.So(a.SetDatabaseConfig(cfg), convey.ShouldBeNil)
			convey.So(a.Validate().Error(), convey.ShouldContainSubstring, "端口")
		})
		convey.Convey("缺 username 应报错", func() {
			a := &Asset{Type: AssetTypeDatabase, Name: "x", GroupID: 1}
			cfg := &DatabaseConfig{
				Driver: DriverMSSQL, Host: "localhost",
				Port: 1433,
			}
			convey.So(a.SetDatabaseConfig(cfg), convey.ShouldBeNil)
			convey.So(a.Validate().Error(), convey.ShouldContainSubstring, "用户名")
		})
		convey.Convey("完整字段通过", func() {
			a := &Asset{Type: AssetTypeDatabase, Name: "x", GroupID: 1}
			cfg := &DatabaseConfig{
				Driver: DriverMSSQL, Host: "localhost",
				Port: 1433, Username: "sa",
			}
			convey.So(a.SetDatabaseConfig(cfg), convey.ShouldBeNil)
			convey.So(a.Validate(), convey.ShouldBeNil)
		})
	})
}

func TestValidateDatabaseSQLite(t *testing.T) {
	convey.Convey("SQLite driver validation", t, func() {
		convey.Convey("缺 path 应报错", func() {
			a := &Asset{Type: AssetTypeDatabase, Name: "x", GroupID: 1}
			cfg := &DatabaseConfig{Driver: DriverSQLite}
			convey.So(a.SetDatabaseConfig(cfg), convey.ShouldBeNil)
			convey.So(a.Validate().Error(), convey.ShouldContainSubstring, "path")
		})
		convey.Convey("path 非绝对路径应报错", func() {
			a := &Asset{Type: AssetTypeDatabase, Name: "x", GroupID: 1}
			cfg := &DatabaseConfig{Driver: DriverSQLite, Path: "relative.db"}
			convey.So(a.SetDatabaseConfig(cfg), convey.ShouldBeNil)
			convey.So(a.Validate().Error(), convey.ShouldContainSubstring, "绝对路径")
		})
		convey.Convey("SQLite 不允许 SSH 隧道", func() {
			a := &Asset{
				Type: AssetTypeDatabase, Name: "x", GroupID: 1,
				SSHTunnelID: 5,
			}
			cfg := &DatabaseConfig{Driver: DriverSQLite, Path: "/tmp/x.db"}
			convey.So(a.SetDatabaseConfig(cfg), convey.ShouldBeNil)
			convey.So(a.Validate().Error(), convey.ShouldContainSubstring, "隧道")
		})
		convey.Convey("远端 SQLite VFS 必须指定 SSH 资产", func() {
			a := &Asset{Type: AssetTypeDatabase, Name: "x", GroupID: 1}
			cfg := &DatabaseConfig{Driver: DriverSQLite, SQLiteSource: SQLiteSourceRemoteSSHVFS, Path: "/tmp/x.db"}
			convey.So(a.SetDatabaseConfig(cfg), convey.ShouldBeNil)
			convey.So(a.Validate().Error(), convey.ShouldContainSubstring, "SSH")
		})
		convey.Convey("远端 SQLite VFS 允许 SSH 资产", func() {
			a := &Asset{
				Type: AssetTypeDatabase, Name: "x", GroupID: 1,
				SSHTunnelID: 5,
			}
			cfg := &DatabaseConfig{Driver: DriverSQLite, SQLiteSource: SQLiteSourceRemoteSSHVFS, Path: "/tmp/x.db"}
			convey.So(a.SetDatabaseConfig(cfg), convey.ShouldBeNil)
			convey.So(a.Validate(), convey.ShouldBeNil)
		})
		convey.Convey("绝对路径 + 无隧道通过", func() {
			a := &Asset{Type: AssetTypeDatabase, Name: "x", GroupID: 1}
			cfg := &DatabaseConfig{Driver: DriverSQLite, Path: "/tmp/x.db"}
			convey.So(a.SetDatabaseConfig(cfg), convey.ShouldBeNil)
			convey.So(a.Validate(), convey.ShouldBeNil)
		})
	})
}

func TestAsset_IsEtcd(t *testing.T) {
	convey.Convey("IsEtcd", t, func() {
		a := &Asset{Type: AssetTypeEtcd}
		assert.True(t, a.IsEtcd())
		assert.False(t, a.IsRedis())
	})
}

func TestAsset_GetSetEtcdConfig(t *testing.T) {
	convey.Convey("Get/SetEtcdConfig 往返", t, func() {
		a := &Asset{Type: AssetTypeEtcd}
		cfg := &EtcdConfig{
			Endpoints: []string{"10.0.0.1:2379", "10.0.0.2:2379"},
			Username:  "root",
			TLS:       true,
		}
		assert.NoError(t, a.SetEtcdConfig(cfg))
		got, err := a.GetEtcdConfig()
		assert.NoError(t, err)
		assert.Equal(t, cfg.Endpoints, got.Endpoints)
		assert.Equal(t, cfg.Username, got.Username)
		assert.True(t, got.TLS)
	})

	convey.Convey("GetEtcdConfig 非 etcd 资产返回错误", t, func() {
		a := &Asset{Type: AssetTypeSSH}
		_, err := a.GetEtcdConfig()
		assert.Error(t, err)
	})
}

func TestAsset_ValidateEtcd(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *EtcdConfig
		wantErr bool
	}{
		{"valid single", &EtcdConfig{Endpoints: []string{"127.0.0.1:2379"}}, false},
		{"valid cluster", &EtcdConfig{Endpoints: []string{"10.0.0.1:2379", "10.0.0.2:2379"}}, false},
		{"empty endpoints", &EtcdConfig{}, true},
		{"endpoint missing port", &EtcdConfig{Endpoints: []string{"10.0.0.1"}}, true},
		{"endpoint invalid port", &EtcdConfig{Endpoints: []string{"10.0.0.1:abc"}}, true},
		{"endpoint port out of range", &EtcdConfig{Endpoints: []string{"10.0.0.1:70000"}}, true},
		{"endpoint missing host", &EtcdConfig{Endpoints: []string{":2379"}}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := &Asset{Name: "test", Type: AssetTypeEtcd}
			assert.NoError(t, a.SetEtcdConfig(tc.cfg))
			err := a.Validate()
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestAsset_CanConnectEtcd(t *testing.T) {
	convey.Convey("etcd 端点非空且 Status=Active 可连接", t, func() {
		a := &Asset{Type: AssetTypeEtcd, Status: StatusActive}
		assert.NoError(t, a.SetEtcdConfig(&EtcdConfig{Endpoints: []string{"127.0.0.1:2379"}}))
		assert.True(t, a.CanConnect())
	})
	convey.Convey("etcd 端点为空不可连接", t, func() {
		a := &Asset{Type: AssetTypeEtcd, Status: StatusActive}
		assert.NoError(t, a.SetEtcdConfig(&EtcdConfig{Endpoints: nil}))
		assert.False(t, a.CanConnect())
	})
	convey.Convey("etcd Status 非 Active 不可连接", t, func() {
		a := &Asset{Type: AssetTypeEtcd, Status: StatusDeleted}
		assert.NoError(t, a.SetEtcdConfig(&EtcdConfig{Endpoints: []string{"127.0.0.1:2379"}}))
		assert.False(t, a.CanConnect())
	})
}

func TestAsset_GetSetEtcdPolicy(t *testing.T) {
	convey.Convey("Get/SetEtcdPolicy 往返", t, func() {
		a := &Asset{Type: AssetTypeEtcd}
		p := &EtcdPolicy{
			AllowList: []string{"get *", "put *"},
			DenyList:  []string{"member remove *"},
		}
		assert.NoError(t, a.SetEtcdPolicy(p))
		got, err := a.GetEtcdPolicy()
		assert.NoError(t, err)
		assert.Equal(t, p.AllowList, got.AllowList)
		assert.Equal(t, p.DenyList, got.DenyList)
	})

	convey.Convey("SetEtcdPolicy 空策略清空字段", t, func() {
		a := &Asset{Type: AssetTypeEtcd}
		assert.NoError(t, a.SetEtcdPolicy(&EtcdPolicy{AllowList: []string{"get *"}}))
		assert.NotEmpty(t, a.CmdPolicy)
		assert.NoError(t, a.SetEtcdPolicy(&EtcdPolicy{}))
		assert.Empty(t, a.CmdPolicy)
	})

	convey.Convey("CmdPolicy 为空时 GetEtcdPolicy 返回零值", t, func() {
		a := &Asset{Type: AssetTypeEtcd}
		got, err := a.GetEtcdPolicy()
		assert.NoError(t, err)
		assert.NotNil(t, got)
		assert.Empty(t, got.AllowList)
		assert.Empty(t, got.DenyList)
		assert.Empty(t, got.Groups)
	})
}
