import java.util.Properties

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

// Version and release signing come from the environment (scripts/build-android.sh
// and CI). Keys are never in the repository.
val appVersion: String = System.getenv("ESL_VERSION")?.removePrefix("v")?.takeIf { it.isNotBlank() } ?: "0.0.0-dev"
// 1.2.3 → 10203, so each release installs over the one before it.
val appVersionCode: Int = appVersion.substringBefore('-').split('.')
    .map { it.toIntOrNull() ?: 0 }.let { p -> p.getOrElse(0) { 0 } * 10000 + p.getOrElse(1) { 0 } * 100 + p.getOrElse(2) { 0 } }
    .coerceAtLeast(1)

val signing: Properties? = rootProject.file("keystore.properties").takeIf { it.isFile }
    ?.let { f -> Properties().apply { f.inputStream().use { load(it) } } }

android {
    namespace = "io.github.blaineam.enterspacelight"
    compileSdk = 35

    defaultConfig {
        applicationId = "io.github.blaineam.enterspacelight"
        minSdk = 26
        targetSdk = 35
        versionCode = appVersionCode
        versionName = appVersion
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
    }
    signingConfigs {
        if (signing != null) create("release") {
            storeFile = file(signing.getProperty("storeFile"))
            storePassword = signing.getProperty("storePassword")
            keyAlias = signing.getProperty("keyAlias")
            keyPassword = signing.getProperty("keyPassword")
        }
    }
    buildTypes {
        release {
            // The engine is native code; shrinking the small Kotlin shell
            // would only risk the names gomobile looks up by reflection.
            isMinifyEnabled = false
            signingConfig = signingConfigs.findByName("release")
        }
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions { jvmTarget = "17" }
    packaging { jniLibs { useLegacyPackaging = true } } // compressed in the APK: a smaller download
    lint {
        // Distributed outside Google Play: all-files access and the
        // special-use service are deliberate (README, Android).
        disable += setOf("ScopedStorage", "ExpiredTargetSdkVersion")
        warningsAsErrors = false
        abortOnError = true
    }
}

dependencies {
    // The Go engine, built by scripts/build-android.sh.
    implementation(files("libs/engine.aar"))
    // Tests only: the app itself has no third-party Java code.
    androidTestImplementation("androidx.test:runner:1.6.2")
    androidTestImplementation("androidx.test.ext:junit:1.2.1")
}
