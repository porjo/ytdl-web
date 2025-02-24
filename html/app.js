
// SSE vars
var reconnectFrequencySeconds = 1; // doubles every retry
var evtSource;

var sseHost = window.location.protocol + "//" + window.location.host;
if(window.location.pathname !== "/") {
	sseHost += window.location.pathname;
}
sseHost = sseHost.replace(/\/$/, "");

console.log("sseHost", sseHost);

var player = null;
var progLast = null;
var seekTimer = null;

var trackId = null;

//var lastPing = new Date();

async function postData (data) {
	try {
		const response = await fetch(sseHost + "/dl", {
			method: "POST",
			body: JSON.stringify(data)
		});
		if (!response.ok) {
			throw new Error(`Response status: ${response.status}`);
		}
	} catch (error) {
		console.error(error.message);
	}
}

$(function(){

	setupEventSource();

	// fetch /recent will trigger event to send recent URLs
	fetch(sseHost + "/recent");

	const url = new URL(window.location);
	const searchParams = new URLSearchParams(url.search);
	var inputURL = searchParams.get('url');
	if (inputURL) {
		$("#url").val(inputURL);
	}

	$("#url").on('paste', function(e) {
		let data = e.originalEvent.clipboardData.getData('Text');
		searchParams.set('url', encodeURI(data));
		url.search = searchParams;
		window.history.pushState('newUrl', '', url);
	});

	$("#go-button").click(function () {
		$("#spinner").show();
		$("#progress-bar > span").css("width", "0%")
			.text("0%");
		let $url = $("#url");
		postData({ url: $url.val() });
		$("#status").prepend("Requesting URL " + url + "\n");
		$url.val('');
		//$(this).prop('disabled', true);
	});

	$("#searchbox span").click(function() {
		$("#url").val('');
	});

	$("#recent_header #controls").click(function() {
		let urls = [];
		$(".recent_url.selected").each(function() {
			urls.push($(this).find(".stream_play").data("stream_url"));
			$(this).remove();
		});

		if( urls.length > 0 ) {
			let param = {delete_urls: urls};
			postData(param);
		}
	});

	function updateJob (msg) {
		let $job = $('#job-' + msg.Value.Id);
		if ($job.length == 0) {
			$job = $('<div>', { id: 'job-' + msg.Value.Id, class: 'job' }).appendTo('#output');
			$('<div>', { class: 'progress-bar', html: '<span>0%</span>' }).appendTo($job);
			$('<div>', {
				class: 'details', html:
					'<label>Video Title:</label><span class="title"></span>' +
					'<br>' +
					'<label>ETA:</label><span class="eta"></span>' +
					'<br>' +
					'<label>Size:</label><span class="filesize"></span>' +
					'<div class="status"></div>'
			}).appendTo($job);
		}

		if (!('Title' in msg.Value)) {
			return $job;
		}

		let title = msg.Value.Title;

		let fileSize = '';
		if ('FileSize' in msg.Value) {
			let bytes = msg.Value.FileSize;
			if( bytes == 0 && 'Progress' in msg.Value) {
				bytes = msg.Value.Progress.FileSize;
			}
			fileSize = (bytes / 1024 / 1024).toFixed(2) + " MB";
		}

		let eta = '';
		let pct = 0;
		if ('Progress' in msg.Value) {
			pct = msg.Value.Progress.Pct > 100 ? 100 : msg.Value.Progress.Pct;
			eta = msg.Value.Progress.ETA;
		}

		$job.find('.title').text(title);
		$job.find('.progress-bar > span').css("width", pct + "%")
			.text(pct.toFixed(1) + "%");
		$job.find('.eta').text(eta);
		$job.find('.filesize').text(fileSize);

		return $job
	}

	function msgHandler(json) {
		//console.log("msgHandler", json);
		let msg = JSON.parse(json);
		if( 'Key' in msg ) {
			switch (msg.Key) {
				case 'error':
					$("#output").show();
					$("#spinner").hide();
					$("#status").prepend("Error: " + msg.Value + "\n");
					break;
				case 'unknown':
					$("#output").show();
					$("#spinner").hide();
					var $job = updateJob(msg);
					$job.find('.status').prepend(msg.Value.Msg);
					break;
				case 'completed':
					$("#spinner").hide();
					var $job = updateJob(msg);
					$job.remove();
					break;
				case 'info':
					$("#output").show();
					$("#spinner").hide();
					var $job = updateJob(msg);
					break;
				case 'link_stream':
					if(!isPlaying()) {
						$("#playa").show();
						updatePlayer(msg.Value.DownloadURL, msg.Value.Title, msg.Value.Artist);
					}
					break;
				case 'recent':
					if( msg.Value.length == 0 ) {
						break;
					}
					$("#recent").show();
					$("#recent_urls").empty();
					for (let i=0; i< msg.Value.length; i++) {
						let artist = msg.Value[i].Artist;
						let title = msg.Value[i].Title;
						// let description = msg.Value[i].Description;
						// if(description.length == 0) description = "n/a";

						let $ru = $("<div>", {class: 'recent_url'});
						let $cont = $("<div>", {class: 'media_meta'});
						$cont.append($("<span>", {class: 'media_artist', text: artist}));
						$cont.append($("<span>", { class: 'media_title', text: title }));
						// let $description = $("<div>", { class: 'media_description', text: description });
						// $cont.append($description);
						$cont.click(function() {
							$(this).closest(".recent_url").toggleClass("selected");
							// $description.slideToggle().addClass('overflow_scroll');
						});
						$ru.append($cont);

						let $media = $("<div>", {class: 'media'});
						$playButton = $("<svg>", {version: "2.0"}).append( $("<use>", {href: "#play-btn"}) );
						let $mediaPlay = $("<div>", {class: 'stream_play', html: $playButton});
						// 'refresh' play button content to allow SVG to display
						$mediaPlay.html($mediaPlay.html());
						$mediaPlay.data("stream_url", msg.Value[i].URL);
						$mediaPlay.data("artist", artist);
						$mediaPlay.data("title", title);
						$mediaPlay.click(streamPlayClick);
						$media.append($mediaPlay);
						const progress = getMediaProgress(title, artist);
						if (progress.duration > 0) {
							const currentTime = new Date(progress.currentTime * 1000).toISOString().slice(11, 19);
							const duration = new Date(progress.duration * 1000).toISOString().slice(11, 19);
							let mediaProgressTxt = currentTime + " / " + duration + " - " + progress.percent.toFixed(0).padStart(3) + "%";
							mediaProgressTxt = mediaProgressTxt.replaceAll(" ", "&nbsp;");
							let $mediaProgress = $("<div>", { class: 'media_progress', html: mediaProgressTxt });
							$media.append($mediaProgress);
						}
						$ru.append($media);
						$("#recent_urls").append($ru);
					}
					break;
			}
		}
	};

	function streamPlayClick() {
		$("#playa").show();
		let url = $(this).data("stream_url");
		let title = $(this).data("title");
		let artist = $(this).data("artist");
		updatePlayer(url, title, artist, true);
	}

	function isPlaying() {
		if(player && !player.audio.paused) {
			return true;
		}
		return false
	}

	function getMediaProgress(title, artist) {
		let trackId = "ytdl-" + title + " - " + artist;
		let obj = JSON.parse(localStorage.getItem(trackId))
		if (obj) {
			return {currentTime: obj.currentTime, duration: obj.duration, percent: (obj.currentTime/obj.duration*100)};
		} else {
			return {currentTime: 0, duration: 0, percent: 0};
		}
	}

	function updatePlayer(url, title, artist, autoplay=false) {

		trackId =  "ytdl-" + title + " - " + artist;
		document.title = trackId;

		if( player === null ) {
			player = new Shikwasa.Player({
				container: () => document.querySelector('#playa'),
				audio: {
					title: title,
					artist: artist,
					src: url
				},
				speedOptions: [1.0, 1.1, 1.2],
				download: true
			});

			player.on('loadedmetadata', (e) => {
				let obj = JSON.parse(localStorage.getItem(trackId))
				if (obj && obj.currentTime > 0) {
					player.seek(obj.currentTime);
					if (obj.playbackRate) {
						player.playbackRate = obj.playbackRate;
					}
				}

				// store latest play time
				seekTimer = setInterval(() => {
					if (isPlaying()) {
						let o = { currentTime: player.currentTime, duration: player.duration, timestamp: new Date().getTime(), playbackRate: player.playbackRate }
						localStorage.setItem(trackId, JSON.stringify(o));
					}
				}, 2000);
			});

		} else {

			if (seekTimer !== null) {
				clearInterval(seekTimer);
			}

			player.update({
				title: title,
				artist: artist,
				src: url
			});
		}

		if(autoplay) {
			player.play();
		}

		// Add space for player at bottom of page
		$("body").css("padding-bottom", $(".shk-player").css("height"));
	}

	$('#output').on('click','.status', function() {
		$(this).toggleClass("expand");
	});


	// handle SSE connection/re-connect
	// Credit: https://stackoverflow.com/a/54385402/202311

	function isFunction (functionToCheck) {
		return functionToCheck && {}.toString.call(functionToCheck) === '[object Function]';
	}

	function debounce (func, wait) {
		var timeout;
		var waitFunc;

		return function () {
			if (isFunction(wait)) {
				waitFunc = wait;
			}
			else {
				waitFunc = function () { return wait };
			}

			var context = this, args = arguments;
			var later = function () {
				timeout = null;
				func.apply(context, args);
			};
			clearTimeout(timeout);
			timeout = setTimeout(later, waitFunc());
		};
	}

	var reconnectFunc = debounce(function () {
		setupEventSource();
		// Double every attempt to avoid overwhelming server
		reconnectFrequencySeconds *= 2;
		// Max out at ~1 minute as a compromise between user experience and server load
		if (reconnectFrequencySeconds >= 64) {
			reconnectFrequencySeconds = 64;
		}
	}, function () {
		console.log("evtSource reconnecting in " + reconnectFrequencySeconds + " secs")
		return reconnectFrequencySeconds * 1000;
	});

	function setupEventSource () {
		evtSource = new EventSource(sseHost + "/sse");

		evtSource.onopen = function (e) {
			// Reset reconnect frequency upon successful connection
			reconnectFrequencySeconds = 1;
		};

		evtSource.onerror = function (e) {
			console.log("evtSource error");
			evtSource.close();
			reconnectFunc();
		};

		/*
		evtSource.addEventListener("ping", (event) => {
			//console.log("ping");
			lastPing = new Date();
		});
		*/

		evtSource.onmessage = function (e) {
			let messages = e.data.split(/\r?\n/);
			messages.forEach(msgHandler)
		}
	}

});

// cleanup storage (run once at start)
for (let i = 0; i < localStorage.length; i++) {
	let name = localStorage.key(i);
	if (!name.startsWith("ytdl-")) {
		continue
	}
	let o = JSON.parse(localStorage.getItem(name));

	if (o) {
		let delta = new Date().getTime() - o.timestamp;
		// remove if older than 7 days
		if (delta > 7 * 86400 * 1000) {
			localStorage.removeItem(name);
		}
	}
}